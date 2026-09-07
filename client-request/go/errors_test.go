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
		name   string
		err    error
		expect func(t *testing.T, retryable bool)
	}{
		{
			name: "request timeout",
			err:  fmt.Errorf("%w: deadline", ErrRequestTimeout),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "internal",
			err:  fmt.Errorf("%w: boom", ErrInternal),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "dfdaemon 500",
			err:  &DfdaemonError{StatusCode: http.StatusInternalServerError},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "dfdaemon 507",
			err:  &DfdaemonError{StatusCode: http.StatusInsufficientStorage},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "dfdaemon 422",
			err:  &DfdaemonError{StatusCode: http.StatusUnprocessableEntity},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "dfdaemon 400",
			err:  &DfdaemonError{StatusCode: http.StatusBadRequest},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "backend 500",
			err:  &BackendError{StatusCode: http.StatusInternalServerError},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "backend 502",
			err:  &BackendError{StatusCode: http.StatusBadGateway},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "backend 503",
			err:  &BackendError{StatusCode: http.StatusServiceUnavailable},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "backend 408",
			err:  &BackendError{StatusCode: http.StatusRequestTimeout},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "backend 429",
			err:  &BackendError{StatusCode: http.StatusTooManyRequests},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "proxy 500",
			err:  &ProxyError{StatusCode: http.StatusInternalServerError},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "proxy 502",
			err:  &ProxyError{StatusCode: http.StatusBadGateway},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "proxy 503",
			err:  &ProxyError{StatusCode: http.StatusServiceUnavailable},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "proxy 408",
			err:  &ProxyError{StatusCode: http.StatusRequestTimeout},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "proxy 429",
			err:  &ProxyError{StatusCode: http.StatusTooManyRequests},
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "wrapped proxy 429",
			err:  fmt.Errorf("wrapped: %w", &ProxyError{StatusCode: http.StatusTooManyRequests}),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "backend 400",
			err:  &BackendError{StatusCode: http.StatusBadRequest},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "backend 401",
			err:  &BackendError{StatusCode: http.StatusUnauthorized},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "backend 403",
			err:  &BackendError{StatusCode: http.StatusForbidden},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "backend 404",
			err:  &BackendError{StatusCode: http.StatusNotFound},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "backend 416",
			err:  &BackendError{StatusCode: http.StatusRequestedRangeNotSatisfiable},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "proxy 400",
			err:  &ProxyError{StatusCode: http.StatusBadRequest},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "proxy 401",
			err:  &ProxyError{StatusCode: http.StatusUnauthorized},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "proxy 403",
			err:  &ProxyError{StatusCode: http.StatusForbidden},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "proxy 404",
			err:  &ProxyError{StatusCode: http.StatusNotFound},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "proxy 416",
			err:  &ProxyError{StatusCode: http.StatusRequestedRangeNotSatisfiable},
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "grpc internal",
			err:  status.Error(codes.Internal, "boom"),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "grpc unavailable",
			err:  status.Error(codes.Unavailable, "boom"),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "grpc unknown",
			err:  status.Error(codes.Unknown, "boom"),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "grpc aborted",
			err:  status.Error(codes.Aborted, "boom"),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "grpc deadline exceeded",
			err:  status.Error(codes.DeadlineExceeded, "boom"),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "wrapped grpc internal",
			err:  fmt.Errorf("%w: failed to download task: %w", ErrInternal, status.Error(codes.Internal, "boom")),
			expect: func(t *testing.T, retryable bool) {
				assert.True(t, retryable)
			},
		},
		{
			name: "grpc invalid argument",
			err:  status.Error(codes.InvalidArgument, "no"),
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "grpc not found",
			err:  status.Error(codes.NotFound, "no"),
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "grpc permission denied",
			err:  status.Error(codes.PermissionDenied, "no"),
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "grpc unauthenticated",
			err:  status.Error(codes.Unauthenticated, "no"),
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "grpc resource exhausted",
			err:  status.Error(codes.ResourceExhausted, "no"),
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "wrapped grpc not found",
			err:  fmt.Errorf("%w: failed to download task: %w", ErrInternal, status.Error(codes.NotFound, "no")),
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
		{
			name: "invalid argument",
			err:  fmt.Errorf("%w: bad", ErrInvalidArgument),
			expect: func(t *testing.T, retryable bool) {
				assert.False(t, retryable)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.expect(t, isRetryable(tc.err))
		})
	}
}
