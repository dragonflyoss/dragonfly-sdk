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
)

var (
	// ErrRequestTimeout indicates the request timed out.
	ErrRequestTimeout = errors.New("request timeout")

	// ErrInvalidArgument indicates an invalid argument.
	ErrInvalidArgument = errors.New("invalid argument")

	// ErrInternal indicates a request internal error.
	ErrInternal = errors.New("request internal error")
)

// BackendError is the error detail returned by the backend server.
type BackendError struct {
	// Message is the backend error message.
	Message string

	// Header is the backend HTTP response header.
	Header map[string]string

	// StatusCode is the backend HTTP status code.
	StatusCode int
}

// Error implements the error interface.
func (e *BackendError) Error() string {
	return fmt.Sprintf("backend server error, message: %q, header: %v, status_code: %d", e.Message, e.Header, e.StatusCode)
}

// ProxyError is the error detail returned by the proxy server.
type ProxyError struct {
	// Message is the proxy error message.
	Message string

	// Header is the proxy HTTP response header.
	Header map[string]string

	// StatusCode is the proxy HTTP status code.
	StatusCode int
}

// Error implements the error interface.
func (e *ProxyError) Error() string {
	return fmt.Sprintf("proxy server error, message: %q, header: %v, status_code: %d", e.Message, e.Header, e.StatusCode)
}

// DfdaemonError is the error detail returned by the dfdaemon.
type DfdaemonError struct {
	// Message is the dfdaemon error message.
	Message string
}

// Error implements the error interface.
func (e *DfdaemonError) Error() string {
	return fmt.Sprintf("dfdaemon error, message: %q", e.Message)
}
