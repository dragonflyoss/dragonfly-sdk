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
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"request timeout", fmt.Errorf("%w: deadline", ErrRequestTimeout), true},
		{"internal", fmt.Errorf("%w: boom", ErrInternal), true},
		{"dfdaemon error", &DfdaemonError{}, true},
		{"backend 500", &BackendError{StatusCode: http.StatusInternalServerError}, true},
		{"backend 502", &BackendError{StatusCode: http.StatusBadGateway}, true},
		{"backend 503", &BackendError{StatusCode: http.StatusServiceUnavailable}, true},
		{"backend 408", &BackendError{StatusCode: http.StatusRequestTimeout}, true},
		{"backend 429", &BackendError{StatusCode: http.StatusTooManyRequests}, true},
		{"proxy 500", &ProxyError{StatusCode: http.StatusInternalServerError}, true},
		{"proxy 502", &ProxyError{StatusCode: http.StatusBadGateway}, true},
		{"proxy 503", &ProxyError{StatusCode: http.StatusServiceUnavailable}, true},
		{"proxy 408", &ProxyError{StatusCode: http.StatusRequestTimeout}, true},
		{"proxy 429", &ProxyError{StatusCode: http.StatusTooManyRequests}, true},
		{"wrapped proxy 429", fmt.Errorf("wrapped: %w", &ProxyError{StatusCode: http.StatusTooManyRequests}), true},
		{"backend 400", &BackendError{StatusCode: http.StatusBadRequest}, false},
		{"backend 401", &BackendError{StatusCode: http.StatusUnauthorized}, false},
		{"backend 403", &BackendError{StatusCode: http.StatusForbidden}, false},
		{"backend 404", &BackendError{StatusCode: http.StatusNotFound}, false},
		{"backend 416", &BackendError{StatusCode: http.StatusRequestedRangeNotSatisfiable}, false},
		{"proxy 400", &ProxyError{StatusCode: http.StatusBadRequest}, false},
		{"proxy 401", &ProxyError{StatusCode: http.StatusUnauthorized}, false},
		{"proxy 403", &ProxyError{StatusCode: http.StatusForbidden}, false},
		{"proxy 404", &ProxyError{StatusCode: http.StatusNotFound}, false},
		{"proxy 416", &ProxyError{StatusCode: http.StatusRequestedRangeNotSatisfiable}, false},
		{"grpc internal", status.Error(codes.Internal, "boom"), true},
		{"grpc unavailable", status.Error(codes.Unavailable, "boom"), true},
		{"grpc unknown", status.Error(codes.Unknown, "boom"), true},
		{"grpc aborted", status.Error(codes.Aborted, "boom"), true},
		{"grpc deadline exceeded", status.Error(codes.DeadlineExceeded, "boom"), true},
		{"wrapped grpc internal", fmt.Errorf("%w: failed to download task: %w", ErrInternal, status.Error(codes.Internal, "boom")), true},
		{"grpc invalid argument", status.Error(codes.InvalidArgument, "no"), false},
		{"grpc not found", status.Error(codes.NotFound, "no"), false},
		{"grpc permission denied", status.Error(codes.PermissionDenied, "no"), false},
		{"grpc unauthenticated", status.Error(codes.Unauthenticated, "no"), false},
		{"grpc resource exhausted", status.Error(codes.ResourceExhausted, "no"), false},
		{"wrapped grpc not found", fmt.Errorf("%w: failed to download task: %w", ErrInternal, status.Error(codes.NotFound, "no")), false},
		{"invalid argument", fmt.Errorf("%w: bad", ErrInvalidArgument), false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, isRetryable(tc.err))
		})
	}
}
