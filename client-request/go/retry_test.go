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
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
)

func TestRetryPolicyNewBackOff(t *testing.T) {
	tests := []struct {
		name        string
		exponential *backoff.ExponentialBackOff
		expected    []time.Duration
	}{
		{"nil", nil, []time.Duration{0, 0, 0}},
		{
			"exponential",
			&backoff.ExponentialBackOff{InitialInterval: time.Millisecond, MaxInterval: 3 * time.Millisecond, Multiplier: 2},
			[]time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			// Every request gets a fresh copy, so two in a row start over.
			for range 2 {
				b := retryPolicy{backoff: tc.exponential}.newBackOff()
				b.Reset()
				for _, expected := range tc.expected {
					assert.Equal(expected, b.NextBackOff())
				}
			}
		})
	}
}

func TestRetry(t *testing.T) {
	transient := fmt.Errorf("%w: boom", ErrInternal)
	definitive := &BackendError{StatusCode: http.StatusNotFound}
	tests := []struct {
		name             string
		maxRetries       uint8
		failures         []error
		expectedAttempts int
		expectedErr      error
	}{
		{"succeeds at once", 3, nil, 1, nil},
		{"retries transient failures", 3, []error{transient, transient}, 3, nil},
		{"stops at a definitive failure", 3, []error{definitive}, 1, definitive},
		{"exhausts the retries", 2, []error{transient, transient, transient}, 3, transient},
		{"no retries", 0, []error{transient}, 1, transient},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			attempts := 0
			policy := retryPolicy{
				maxRetries: tc.maxRetries,
				backoff:    &backoff.ExponentialBackOff{InitialInterval: time.Millisecond, MaxInterval: 2 * time.Millisecond, Multiplier: 2},
			}
			_, err := retry(context.Background(), policy, func() (struct{}, error) {
				attempts++
				if attempts <= len(tc.failures) {
					return struct{}{}, tc.failures[attempts-1]
				}

				return struct{}{}, nil
			})

			if tc.expectedErr == nil {
				assert.NoError(err)
			} else {
				assert.ErrorIs(err, tc.expectedErr)
			}
			assert.Equal(tc.expectedAttempts, attempts)
		})
	}
}

func TestRetryWithEndpoints(t *testing.T) {
	transient := fmt.Errorf("%w: boom", ErrInternal)
	definitive := &BackendError{StatusCode: http.StatusNotFound}
	tests := []struct {
		name             string
		endpoints        []string
		maxRetries       uint8
		failures         []error
		expectedAttempts int
		expectedErr      error
	}{
		{"no endpoints", nil, 3, nil, 0, ErrInvalidArgument},
		{"succeeds at once", []string{"a"}, 3, nil, 1, nil},
		{"retries transient failures on the next endpoint", []string{"a", "b"}, 3, []error{transient, transient}, 3, nil},
		{"wraps around the endpoints", []string{"a", "b", "c"}, 3, []error{transient, transient, transient}, 4, nil},
		{"stops at a definitive failure", []string{"a"}, 3, []error{definitive}, 1, definitive},
		{"exhausts the retries", []string{"a", "b"}, 2, []error{transient, transient, transient}, 3, transient},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)

			var attempts []string
			resp, cancel, err := retryWithEndpoints(context.Background(), retryPolicy{maxRetries: tc.maxRetries}, tc.endpoints, time.Second, func(_ context.Context, endpoint string) (*http.Response, error) {
				attempts = append(attempts, endpoint)
				if len(attempts) <= len(tc.failures) {
					return nil, tc.failures[len(attempts)-1]
				}

				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			})

			if tc.expectedErr == nil {
				assert.NoError(err)
				assert.Equal(http.StatusOK, resp.StatusCode)
				cancel()
			} else {
				assert.ErrorIs(err, tc.expectedErr)
			}
			assert.Len(attempts, tc.expectedAttempts)

			// The first attempts spread over distinct endpoints, later ones
			// wrap around in the same order.
			distinct := min(len(attempts), len(tc.endpoints))
			seen := make(map[string]bool, distinct)
			for _, endpoint := range attempts[:distinct] {
				seen[endpoint] = true
			}
			assert.Len(seen, distinct)
			for i := len(tc.endpoints); i < len(attempts); i++ {
				assert.Equal(attempts[i-len(tc.endpoints)], attempts[i])
			}
		})
	}
}
