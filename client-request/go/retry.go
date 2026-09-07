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
	"math/rand/v2"
	"net/http"
	"slices"
	"time"

	"github.com/cenkalti/backoff/v5"
)

// retryPolicy is the retry policy of the requests of a client.
type retryPolicy struct {
	// maxRetries is the maximum number of retries of a request on a transient
	// failure, each sent to the next seed peer.
	maxRetries uint8

	// backoff is the exponential backoff between the retries, nil retrying at
	// once.
	backoff *backoff.ExponentialBackOff
}

// defaultRetryPolicy retries once at once.
var defaultRetryPolicy = retryPolicy{maxRetries: defaultMaxRetries}

// newBackOff returns the backoff of one request: a fresh copy of the
// exponential backoff, or zero delays when none is set.
func (p retryPolicy) newBackOff() backoff.BackOff {
	if p.backoff == nil {
		return &backoff.ZeroBackOff{}
	}

	b := *p.backoff
	return &b
}

// retry runs the attempt until it succeeds, fails definitively, or the retries
// are exhausted, waiting the backoff delay in between.
func retry[T any](ctx context.Context, policy retryPolicy, attempt func() (T, error)) (T, error) {
	result, err := backoff.Retry(ctx, func() (T, error) {
		result, err := attempt()
		if err != nil && !isRetryable(err) {
			return result, backoff.Permanent(err)
		}

		return result, err
	}, backoff.WithBackOff(policy.newBackOff()), backoff.WithMaxTries(uint(policy.maxRetries)+1), backoff.WithMaxElapsedTime(0))

	var permanent *backoff.PermanentError
	switch {
	case errors.As(err, &permanent):
		return result, permanent.Unwrap()
	case errors.Is(err, context.DeadlineExceeded):
		return result, fmt.Errorf("%w: %v", ErrRequestTimeout, err)
	case errors.Is(err, context.Canceled):
		return result, fmt.Errorf("%w: %v", ErrInternal, err)
	default:
		return result, err
	}
}

// sentResponse is a response of a successful attempt with the cancel function
// of its timeout, released once the body is read.
type sentResponse struct {
	resp   *http.Response
	cancel context.CancelFunc
}

// retryWithEndpoints scatters a request across the endpoints: each attempt
// targets the next endpoint of a shuffled order, wrapping around when the
// endpoints are fewer than the attempts, and a transient failure is retried up
// to maxRetries times after the backoff delay. A definitive failure returns at
// once. Each attempt runs under its own timeout.
func retryWithEndpoints(
	ctx context.Context,
	policy retryPolicy,
	endpoints []string,
	timeout time.Duration,
	send func(ctx context.Context, endpoint string) (*http.Response, error),
) (*http.Response, context.CancelFunc, error) {
	if len(endpoints) == 0 {
		return nil, nil, fmt.Errorf("%w: no endpoints to send request", ErrInvalidArgument)
	}

	shuffled := slices.Clone(endpoints)
	rand.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })

	attempts := 0
	sent, err := retry(ctx, policy, func() (sentResponse, error) {
		endpoint := shuffled[attempts%len(shuffled)]
		attempts++

		attemptCtx, cancel := context.WithTimeout(ctx, timeout)
		resp, err := send(attemptCtx, endpoint)
		if err != nil {
			cancel()
			return sentResponse{}, err
		}

		return sentResponse{resp: resp, cancel: cancel}, nil
	})
	if err != nil {
		return nil, nil, err
	}

	return sent.resp, sent.cancel, nil
}
