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
	"net/http"
	"sync"
)

// sender is the interface for sending a request once to the seed peer proxy
// at an endpoint.
//
// Implemented by the proxy clients, so a body is sent again through the
// client that sent it first.
type sender interface {
	// sendTo sends the request once to the seed peer proxy at endpoint.
	sendTo(ctx context.Context, endpoint string, req *GetRequest) (*http.Response, error)
}

// resumableBody is the streaming body of a response that sends its request
// again on the next seed peer for the bytes left when it breaks off.
//
// A streaming body has already handed out the bytes it read, and the writer of
// GetInto cannot be rewound, so the body sends the request again within the
// same retries, its Range narrowed to the bytes left:
//
//	attempt 1 ──▶ B ──▶ 206 bytes 0-14/15 ──▶ read ──▶ broke off at byte 6 ── wait(delay)
//	attempt 2 ──▶ A ──▶ Range: bytes=6-14
//	                    ├─ 206 bytes 6-14/15 ──▶ read ──▶ ... ──▶ EOF
//	                    └─ any other answer ──▶ wait(delay) ──▶ attempt 3 ──▶ B
//
// That needs the first response to tell its bytes, by the Content-Range of a
// single range or the Content-Length of a whole body. A body without them,
// such as a chunked one, fails when it breaks off.
//
// Like containerd and rclone, a response sent again is matched by its
// Content-Range alone, which must name exactly the bytes left of the same
// size. No validator such as an ETag is compared: upstreams often send none,
// and every seed peer stores the headers of its own origin request. The bytes
// are left to the integrity checks of the caller, such as a digest.
type resumableBody struct {
	// ctx is the context of the body, ending its reopens. It is derived from
	// the context of the request and cancelled by stop once the body ends.
	ctx  context.Context
	stop context.CancelFunc

	// sender is the client that sent the request, sending it again on every
	// reopen.
	sender sender

	// req is the copy of the request the body owns, so the caller is free to
	// reuse its headers. Its Range is narrowed to the remaining bytes on every
	// reopen.
	req GetRequest

	// attempts is the attempts of the request, shared with the one that sent
	// it.
	attempts *attempts

	// total is the bytes the first response carries, nil when it does not
	// tell them and the body cannot be reopened.
	total *contentRange

	// offset is the bytes read so far.
	offset int64

	// mu protects rc, cancel and err, so Close ends a Read in progress.
	mu sync.Mutex

	// rc is the body of the current response.
	rc io.ReadCloser

	// cancel releases the timeout of the current attempt.
	cancel context.CancelFunc

	// err is the error that ended the body, returned by every Read after it.
	err error
}

// newResumableBody returns the body of resp to req, sent again through sender
// within attempts when it breaks off. cancel releases the timeout of the
// attempt that answered resp.
func newResumableBody(ctx context.Context, sender sender, req *GetRequest, attempts *attempts, resp *http.Response, cancel context.CancelFunc) *resumableBody {
	ctx, stop := context.WithCancel(ctx)
	b := &resumableBody{
		ctx:      ctx,
		stop:     stop,
		sender:   sender,
		req:      *req,
		attempts: attempts,
		rc:       resp.Body,
		cancel:   cancel,
	}
	b.total = b.contentRange(resp)

	// Own the headers, since they are narrowed on a reopen long after the
	// caller got the body.
	if b.req.header = req.header.Clone(); b.req.header == nil {
		b.req.header = make(http.Header)
	}

	return b
}

// Read implements io.Reader, reopening the body when it breaks off. An error
// ends the body, and every Read after it returns the same error.
func (b *resumableBody) Read(p []byte) (int, error) {
	for {
		b.mu.Lock()
		rc, err := b.rc, b.err
		b.mu.Unlock()
		if err != nil {
			return 0, err
		}

		n, err := rc.Read(p)
		b.offset += int64(n)
		if err == nil {
			return n, nil
		}

		// Tell a body read to its end from one that broke off.
		remaining := b.remaining()
		switch {
		case err == io.EOF && remaining == nil:
			return n, b.end(io.EOF)
		case err == io.EOF:
			err = fmt.Errorf("%w: response body ended without %s", ErrInternal, remaining.httpRange)
		case errors.Is(err, context.DeadlineExceeded):
			err = fmt.Errorf("%w: %w", ErrRequestTimeout, err)
		default:
			err = fmt.Errorf("%w: failed to read response body: %w", ErrInternal, err)
		}

		if err := b.reopen(remaining, err); err != nil {
			return n, b.end(err)
		}

		if n > 0 {
			return n, nil
		}
	}
}

// Close implements io.Closer. It ends the body, a Read or a reopen in
// progress included, and releases the current response.
func (b *resumableBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = http.ErrBodyReadAfterClose
	}

	b.stop()
	b.cancel()
	return b.rc.Close()
}

// copyTo writes the body into w for GetInto. A failed read returns the error
// that ended the body, a failed write is wrapped in ErrInternal.
func (b *resumableBody) copyTo(w io.Writer) error {
	_, err := io.Copy(w, b)
	if err == nil {
		return nil
	}

	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil && b.err != io.EOF {
		return b.err
	}

	return fmt.Errorf("%w: failed to write response body: %w", ErrInternal, err)
}

// end ends the body with err, unless it already ended, and releases its
// context and the timeout of the current attempt. It returns the error that
// ended the body.
func (b *resumableBody) end(err error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err == nil {
		b.err = err
	}

	b.stop()
	b.cancel()
	return b.err
}

// reopen sends the request again for the remaining bytes on the next
// endpoints within the retries left, failing with err when no byte or retry
// is left.
func (b *resumableBody) reopen(remaining *contentRange, err error) error {
	if remaining == nil {
		return err
	}

	delay, ok := b.attempts.backoff()
	if !ok {
		return err
	}

	// Release the response that broke off before sending the request again.
	b.mu.Lock()
	b.rc.Close()
	b.cancel()
	b.mu.Unlock()
	if err := b.attempts.sleep(b.ctx, delay); err != nil {
		return err
	}

	// Narrow the range of the request to the remaining bytes.
	b.req.header.Set("Range", remaining.httpRange.String())

	// Send the request again until a response resumes the body, a response
	// of other bytes failing like an unreachable seed peer.
	resp, cancel, err := b.attempts.run(b.ctx, func(ctx context.Context, endpoint string) (*http.Response, error) {
		resp, err := b.sender.sendTo(ctx, endpoint, &b.req)
		if err != nil {
			return nil, err
		}

		if resumed := b.contentRange(resp); resumed == nil || *resumed != *remaining {
			resp.Body.Close()
			return nil, fmt.Errorf("%w: response sent again does not resume the body", ErrInternal)
		}

		return resp, nil
	})
	if err != nil {
		return err
	}

	// Drop the response if Close ended the body while it was sent again.
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		resp.Body.Close()
		cancel()
		return b.err
	}

	b.rc, b.cancel = resp.Body, cancel
	return nil
}

// remaining returns the bytes left of the ones the first response carries,
// nil when it does not tell them or none is left.
func (b *resumableBody) remaining() *contentRange {
	if b.total == nil || b.offset >= b.total.httpRange.length {
		return nil
	}

	remaining := *b.total
	remaining.httpRange.start += b.offset
	remaining.httpRange.length -= b.offset
	return &remaining
}

// contentRange returns the bytes resp carries: the Content-Range of a 206, the
// whole Content-Length of a 200, nil otherwise.
func (b *resumableBody) contentRange(resp *http.Response) *contentRange {
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		parsed, err := parseContentRange(resp.Header.Get("Content-Range"))
		if err != nil {
			return nil
		}

		return &parsed
	case resp.StatusCode == http.StatusOK && resp.ContentLength > 0:
		return &contentRange{
			httpRange: httpRange{length: resp.ContentLength},
			size:      resp.ContentLength,
		}
	default:
		return nil
	}
}
