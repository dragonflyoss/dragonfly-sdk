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
	retryable := func(t *testing.T, retryable bool) { assert.True(t, retryable) }
	definitive := func(t *testing.T, retryable bool) { assert.False(t, retryable) }
	tests := []struct {
		name   string
		err    error
		expect func(t *testing.T, retryable bool)
	}{
		{"request timeout", fmt.Errorf("%w: deadline", ErrRequestTimeout), retryable},
		{"internal", fmt.Errorf("%w: boom", ErrInternal), retryable},
		{"dfdaemon error", &DfdaemonError{}, retryable},
		{"backend 500", &BackendError{StatusCode: http.StatusInternalServerError}, retryable},
		{"backend 502", &BackendError{StatusCode: http.StatusBadGateway}, retryable},
		{"backend 503", &BackendError{StatusCode: http.StatusServiceUnavailable}, retryable},
		{"backend 408", &BackendError{StatusCode: http.StatusRequestTimeout}, retryable},
		{"backend 429", &BackendError{StatusCode: http.StatusTooManyRequests}, retryable},
		{"proxy 500", &ProxyError{StatusCode: http.StatusInternalServerError}, retryable},
		{"proxy 502", &ProxyError{StatusCode: http.StatusBadGateway}, retryable},
		{"proxy 503", &ProxyError{StatusCode: http.StatusServiceUnavailable}, retryable},
		{"proxy 408", &ProxyError{StatusCode: http.StatusRequestTimeout}, retryable},
		{"proxy 429", &ProxyError{StatusCode: http.StatusTooManyRequests}, retryable},
		{"wrapped proxy 429", fmt.Errorf("wrapped: %w", &ProxyError{StatusCode: http.StatusTooManyRequests}), retryable},
		{"backend 400", &BackendError{StatusCode: http.StatusBadRequest}, definitive},
		{"backend 401", &BackendError{StatusCode: http.StatusUnauthorized}, definitive},
		{"backend 403", &BackendError{StatusCode: http.StatusForbidden}, definitive},
		{"backend 404", &BackendError{StatusCode: http.StatusNotFound}, definitive},
		{"backend 416", &BackendError{StatusCode: http.StatusRequestedRangeNotSatisfiable}, definitive},
		{"proxy 400", &ProxyError{StatusCode: http.StatusBadRequest}, definitive},
		{"proxy 401", &ProxyError{StatusCode: http.StatusUnauthorized}, definitive},
		{"proxy 403", &ProxyError{StatusCode: http.StatusForbidden}, definitive},
		{"proxy 404", &ProxyError{StatusCode: http.StatusNotFound}, definitive},
		{"proxy 416", &ProxyError{StatusCode: http.StatusRequestedRangeNotSatisfiable}, definitive},
		{"grpc internal", status.Error(codes.Internal, "boom"), retryable},
		{"grpc unavailable", status.Error(codes.Unavailable, "boom"), retryable},
		{"grpc unknown", status.Error(codes.Unknown, "boom"), retryable},
		{"grpc aborted", status.Error(codes.Aborted, "boom"), retryable},
		{"grpc deadline exceeded", status.Error(codes.DeadlineExceeded, "boom"), retryable},
		{"wrapped grpc internal", fmt.Errorf("%w: failed to download task: %w", ErrInternal, status.Error(codes.Internal, "boom")), retryable},
		{"grpc invalid argument", status.Error(codes.InvalidArgument, "no"), definitive},
		{"grpc not found", status.Error(codes.NotFound, "no"), definitive},
		{"grpc permission denied", status.Error(codes.PermissionDenied, "no"), definitive},
		{"grpc unauthenticated", status.Error(codes.Unauthenticated, "no"), definitive},
		{"grpc resource exhausted", status.Error(codes.ResourceExhausted, "no"), definitive},
		{"wrapped grpc not found", fmt.Errorf("%w: failed to download task: %w", ErrInternal, status.Error(codes.NotFound, "no")), definitive},
		{"invalid argument", fmt.Errorf("%w: bad", ErrInvalidArgument), definitive},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.expect(t, isRetryable(tc.err))
		})
	}
}
