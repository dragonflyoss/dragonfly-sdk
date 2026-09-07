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
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// ErrRequestTimeout indicates the request timed out.
	ErrRequestTimeout = errors.New("request timeout")

	// ErrInvalidArgument indicates an invalid argument.
	ErrInvalidArgument = errors.New("invalid argument")

	// ErrInternal indicates a request internal error.
	ErrInternal = errors.New("request internal error")
)

// isRetryable reports whether a failed attempt is worth another one against a
// different seed peer.
//
//	| Failure                                                        | Retry |
//	|----------------------------------------------------------------|-------|
//	| ErrRequestTimeout, ErrInternal (connect, transport)            | yes   |
//	| proxy / backend / dfdaemon answer 5xx, 408 or 429              | yes   |
//	| gRPC Internal, Unavailable, Unknown, Aborted, DeadlineExceeded | yes   |
//	| any other answer, such as 403, 404, 422                        | no    |
//	| any other gRPC code, ErrInvalidArgument                        | no    |
//
// 429 and 507 are retried because another seed peer may not be rate limited
// or out of space. Any other 4xx is definitive: every seed peer would answer
// the same.
func isRetryable(err error) bool {
	var answer interface{ retryable() bool }
	if errors.As(err, &answer) {
		return answer.retryable()
	}

	if st, ok := status.FromError(err); ok {
		switch st.Code() {
		case codes.Internal, codes.Unavailable, codes.Unknown, codes.Aborted, codes.DeadlineExceeded:
			return true
		}

		return false
	}

	return errors.Is(err, ErrRequestTimeout) || errors.Is(err, ErrInternal)
}

// isRetryableStatus reports whether the status of a proxy, backend or
// dfdaemon answer marks a transient failure: 5xx, 408 or 429.
func isRetryableStatus(status int) bool {
	return status >= http.StatusInternalServerError ||
		status == http.StatusRequestTimeout ||
		status == http.StatusTooManyRequests
}

// BackendError is the error detail returned by the backend server.
type BackendError struct {
	// Message is the backend error message.
	Message string

	// Header is the backend HTTP response header.
	Header http.Header

	// StatusCode is the backend HTTP status code.
	StatusCode int
}

// Error implements the error interface.
func (e *BackendError) Error() string {
	return fmt.Sprintf("backend server error, message: %q, header: %v, status_code: %d", e.Message, e.Header, e.StatusCode)
}

// retryable reports whether the answer marks a transient failure.
func (e *BackendError) retryable() bool {
	return isRetryableStatus(e.StatusCode)
}

// ProxyError is the error detail returned by the proxy server.
type ProxyError struct {
	// Message is the proxy error message.
	Message string

	// Header is the proxy HTTP response header.
	Header http.Header

	// StatusCode is the proxy HTTP status code.
	StatusCode int
}

// Error implements the error interface.
func (e *ProxyError) Error() string {
	return fmt.Sprintf("proxy server error, message: %q, header: %v, status_code: %d", e.Message, e.Header, e.StatusCode)
}

// retryable reports whether the answer marks a transient failure.
func (e *ProxyError) retryable() bool {
	return isRetryableStatus(e.StatusCode)
}

// DfdaemonError is the error detail returned by the dfdaemon: the seed peer's
// dfdaemon could not run the download task. The status code says why.
//
//	| Status | Meaning                                                           |
//	|--------|-------------------------------------------------------------------|
//	| 400    | invalid request or URL                                            |
//	| 422    | the origin sent no Content-Length, or the piece length is invalid |
//	| 507    | the seed peer has no room for the task                            |
//	| 500    | any other failure: scheduling, peers, streaming                   |
type DfdaemonError struct {
	// Message is the dfdaemon error message.
	Message string

	// Header is the dfdaemon HTTP response header.
	Header http.Header

	// StatusCode is the dfdaemon HTTP status code.
	StatusCode int
}

// Error implements the error interface.
func (e *DfdaemonError) Error() string {
	return fmt.Sprintf("dfdaemon error, message: %q, header: %v, status_code: %d", e.Message, e.Header, e.StatusCode)
}

// retryable reports whether the answer marks a transient failure.
func (e *DfdaemonError) retryable() bool {
	return isRetryableStatus(e.StatusCode)
}
