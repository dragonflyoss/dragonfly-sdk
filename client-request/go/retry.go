/*
 *     Copyright 2026 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package request

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// retryPolicy is how often a request is retried and how long each retry
// waits.
//
//	| Field      | Default | Meaning                                                   |
//	|------------|---------|-----------------------------------------------------------|
//	| maxRetries | 1       | retries after the first attempt, each on the next seed peer |
//	| backoff    | nil     | exponential delay before each retry, nil retries at once  |
//
// The backoff's own limits are ignored in favor of maxRetries, so the two
// knobs cannot disagree.
type retryPolicy struct {
	// maxRetries is the retries after the first attempt, each on the next seed
	// peer.
	maxRetries uint8

	// backoff is the exponential delay before each retry, nil retrying at once.
	backoff *backoff.ExponentialBackOff
}

// defaultRetryPolicy is the default policy: one retry, at once.
var defaultRetryPolicy = retryPolicy{maxRetries: defaultMaxRetries}

// newBackOff returns the backoff of one request: a fresh copy of the
// exponential backoff, since a BackOff carries state across NextBackOff calls,
// or zero delays when none is set.
func (p retryPolicy) newBackOff() backoff.BackOff {
	if p.backoff == nil {
		return &backoff.ZeroBackOff{}
	}

	b := *p.backoff
	return &b
}

// run runs attempt until it succeeds, fails definitively, or has been retried
// maxRetries times, that is at most 1 + maxRetries attempts in total. A
// cancelled context ends the retries with ErrInternal, an expired one with
// ErrRequestTimeout.
//
//	attempt ──nil──────────────────────────────────────────▶ nil
//	   │
//	   └─err──▶ isRetryable(err)? ──no─────────────────────▶ err
//	                 │ yes
//	                 └─▶ retried < maxRetries? ──no────────▶ err
//	                          │ yes
//	                          └─▶ retried++, wait(delay) ──▶ attempt
func (p retryPolicy) run(ctx context.Context, attempt func() error) error {
	_, err := backoff.Retry(ctx, func() (struct{}, error) {
		err := attempt()
		if err != nil && !isRetryable(err) {
			return struct{}{}, backoff.Permanent(err)
		}

		return struct{}{}, err
	}, backoff.WithBackOff(p.newBackOff()), backoff.WithMaxTries(uint(p.maxRetries)+1), backoff.WithMaxElapsedTime(0))

	var permanent *backoff.PermanentError
	switch {
	case errors.As(err, &permanent):
		return permanent.Unwrap()
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %v", ErrRequestTimeout, err)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: %v", ErrInternal, err)
	default:
		return err
	}
}

// newAttempts returns the attempts of a request across endpoints, shuffled
// once, each under its own timeout and read timeout. It fails with
// ErrInvalidArgument when endpoints is empty.
func (p retryPolicy) newAttempts(endpoints []string, timeout, readTimeout time.Duration) (*attempts, error) {
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("%w: no endpoints to send request", ErrInvalidArgument)
	}

	shuffled := slices.Clone(endpoints)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
	delays := p.newBackOff()
	delays.Reset()
	return &attempts{
		endpoints:   shuffled,
		retries:     p.maxRetries,
		delays:      delays,
		timeout:     timeout,
		readTimeout: readTimeout,
	}, nil
}

// attempts is the attempts of one request across the endpoints serving it: the
// shuffled endpoints, the next one to aim at and the retries left. It keeps
// them across runs, so a streamed body that breaks off is sent again within
// the same maxRetries.
type attempts struct {
	// endpoints is the endpoints in the shuffled order the attempts follow.
	endpoints []string

	// next is the index of the next attempt, wrapping around the endpoints.
	next int

	// retries is the retries left.
	retries uint8

	// delays is the delays of the retries left.
	delays backoff.BackOff

	// timeout is the timeout of each attempt.
	timeout time.Duration

	// readTimeout is the read timeout of each attempt.
	readTimeout time.Duration
}

// run runs send until it succeeds, fails definitively, or no retry is left,
// every attempt aimed at the next endpoint, wrapping around when the endpoints
// run out before the retries do. Each attempt runs under its own timeout and
// read timeout, and the cancel function of the successful attempt is returned
// so the caller releases it once the body is read. The loop is written out
// because backoff.Retry resets its backoff and counts its own tries on every
// call.
//
//	endpoints [B, A]                  maxRetries 3
//
//	attempt 1 ──▶ B ──▶ 503 transient  ── wait(delay 1)
//	attempt 2 ──▶ A ──▶ timeout        ── wait(delay 2)
//	attempt 3 ──▶ B ──▶ 404 definitive ──▶ err
func (a *attempts) run(
	ctx context.Context,
	send func(ctx context.Context, endpoint string) (*http.Response, error),
) (*http.Response, context.CancelFunc, error) {
	for {
		endpoint := a.endpoints[a.next%len(a.endpoints)]
		a.next++

		// A timer cancels the attempt on a read timeout, since a deadline would
		// count the time between two reads.
		attemptCtx, cancel := context.WithTimeout(ctx, a.timeout)
		attemptCtx, cancelCause := context.WithCancelCause(attemptCtx)
		timer := time.AfterFunc(a.readTimeout, func() { cancelCause(context.DeadlineExceeded) })
		resp, err := send(attemptCtx, endpoint)
		timer.Stop()
		if err == nil {
			resp.Body = &timeoutReadCloser{ReadCloser: resp.Body, ctx: attemptCtx, timer: timer, timeout: a.readTimeout}
			return resp, cancel, nil
		}
		cancel()

		if !isRetryable(err) {
			return nil, nil, err
		}

		delay, ok := a.backoff()
		if !ok {
			return nil, nil, err
		}

		if err := a.sleep(ctx, delay); err != nil {
			return nil, nil, err
		}
	}
}

// timeoutReadCloser is a response body with a timeout on every Read.
type timeoutReadCloser struct {
	io.ReadCloser

	// ctx is the context of the request.
	ctx context.Context

	// timer cancels ctx once a Read outlasts timeout.
	timer *time.Timer

	// timeout is the timeout of a Read.
	timeout time.Duration
}

// Read implements io.Reader under the timeout.
func (r *timeoutReadCloser) Read(p []byte) (int, error) {
	r.timer.Reset(r.timeout)
	defer r.timer.Stop()

	n, err := r.ReadCloser.Read(p)
	if err != nil && err != io.EOF && errors.Is(context.Cause(r.ctx), context.DeadlineExceeded) {
		err = context.DeadlineExceeded
	}

	return n, err
}

// backoff returns the delay of the next retry after an attempt failed, in run
// or outside it, such as a body that broke off, and false once no retry is
// left.
func (a *attempts) backoff() (time.Duration, bool) {
	if a.retries == 0 {
		return 0, false
	}

	a.retries--
	return a.delays.NextBackOff(), true
}

// sleep waits for the delay of a retry. Like run, a cancelled context ends it
// with ErrInternal, an expired one with ErrRequestTimeout and one ended with
// another cause with that cause.
func (a *attempts) sleep(ctx context.Context, delay time.Duration) error {
	// Check the context first, so a zero delay never outruns it.
	if ctx.Err() == nil {
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
		}
	}

	switch err := context.Cause(ctx); {
	case errors.Is(err, context.DeadlineExceeded):
		return fmt.Errorf("%w: %v", ErrRequestTimeout, err)
	case errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: %v", ErrInternal, err)
	default:
		return err
	}
}
