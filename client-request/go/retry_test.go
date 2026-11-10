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
		expect      func(t *testing.T, b backoff.BackOff)
	}{
		{
			name: "nil",
			expect: func(t *testing.T, b backoff.BackOff) {
				assert := assert.New(t)
				for range 3 {
					assert.Equal(time.Duration(0), b.NextBackOff())
				}
			},
		},
		{
			name:        "exponential",
			exponential: &backoff.ExponentialBackOff{InitialInterval: time.Millisecond, MaxInterval: 3 * time.Millisecond, Multiplier: 2},
			expect: func(t *testing.T, b backoff.BackOff) {
				assert := assert.New(t)
				assert.Equal(time.Millisecond, b.NextBackOff())
				assert.Equal(2*time.Millisecond, b.NextBackOff())
				assert.Equal(3*time.Millisecond, b.NextBackOff())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			policy := retryPolicy{backoff: tc.exponential}
			for range 2 {
				b := policy.newBackOff()
				b.Reset()
				tc.expect(t, b)
			}
		})
	}
}

func TestRetryPolicyRun(t *testing.T) {
	transient := fmt.Errorf("%w: boom", ErrInternal)
	definitive := &BackendError{StatusCode: http.StatusNotFound}
	tests := []struct {
		name       string
		maxRetries uint8
		failures   []error
		expect     func(t *testing.T, attempts int, err error)
	}{
		{
			name:       "succeeds at once",
			maxRetries: 3,
			expect: func(t *testing.T, attempts int, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal(1, attempts)
			},
		},
		{
			name:       "retries transient failures",
			maxRetries: 3,
			failures:   []error{transient, transient},
			expect: func(t *testing.T, attempts int, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal(3, attempts)
			},
		},
		{
			name:       "stops at a definitive failure",
			maxRetries: 3,
			failures:   []error{definitive},
			expect: func(t *testing.T, attempts int, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, definitive)
				assert.Equal(1, attempts)
			},
		},
		{
			name:       "exhausts the retries",
			maxRetries: 2,
			failures:   []error{transient, transient, transient},
			expect: func(t *testing.T, attempts int, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, transient)
				assert.Equal(3, attempts)
			},
		},
		{
			name:       "no retries",
			maxRetries: 0,
			failures:   []error{transient},
			expect: func(t *testing.T, attempts int, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, transient)
				assert.Equal(1, attempts)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			attempts := 0
			policy := retryPolicy{
				maxRetries: tc.maxRetries,
				backoff:    &backoff.ExponentialBackOff{InitialInterval: time.Millisecond, MaxInterval: 2 * time.Millisecond, Multiplier: 2},
			}
			err := policy.run(context.Background(), func() error {
				attempts++
				if attempts <= len(tc.failures) {
					return tc.failures[attempts-1]
				}

				return nil
			})
			tc.expect(t, attempts, err)
		})
	}
}

func TestRetryPolicyRunWaitsTheBackoffDelays(t *testing.T) {
	delay := 20 * time.Millisecond
	tests := []struct {
		name    string
		backoff *backoff.ExponentialBackOff
		expect  func(t *testing.T, elapsed time.Duration)
	}{
		{
			name: "without backoff",
			expect: func(t *testing.T, elapsed time.Duration) {
				assert.Less(t, elapsed, delay)
			},
		},
		{
			name:    "exponential",
			backoff: &backoff.ExponentialBackOff{InitialInterval: delay, MaxInterval: 2 * delay, Multiplier: 2},
			expect: func(t *testing.T, elapsed time.Duration) {
				assert := assert.New(t)
				assert.GreaterOrEqual(elapsed, 3*delay)
				assert.Less(elapsed, 6*delay)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			policy := retryPolicy{maxRetries: 2, backoff: tc.backoff}

			start := time.Now()
			err := policy.run(context.Background(), func() error {
				return fmt.Errorf("%w: boom", ErrInternal)
			})
			assert.ErrorIs(err, ErrInternal)
			tc.expect(t, time.Since(start))

			start = time.Now()
			attempts, err := policy.newAttempts([]string{"a"}, time.Second)
			assert.NoError(err)

			_, _, err = attempts.run(context.Background(), func(context.Context, string) (*http.Response, error) {
				return nil, fmt.Errorf("%w: boom", ErrInternal)
			})
			assert.ErrorIs(err, ErrInternal)
			tc.expect(t, time.Since(start))
		})
	}
}

func TestRetryPolicyRunStopsWhenTheContextEnds(t *testing.T) {
	tests := []struct {
		name   string
		cause  error
		expect func(t *testing.T, attempts int, err error)
	}{
		{
			name:  "cancelled",
			cause: context.Canceled,
			expect: func(t *testing.T, attempts int, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInternal)
				assert.Equal(1, attempts)
			},
		},
		{
			name:  "deadline exceeded",
			cause: context.DeadlineExceeded,
			expect: func(t *testing.T, attempts int, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrRequestTimeout)
				assert.Equal(1, attempts)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)

			attempts := 0
			policy := retryPolicy{
				maxRetries: 5,
				backoff:    &backoff.ExponentialBackOff{InitialInterval: time.Second, MaxInterval: time.Second, Multiplier: 1},
			}
			err := policy.run(ctx, func() error {
				attempts++
				cancel(tc.cause)
				return fmt.Errorf("%w: boom", ErrInternal)
			})
			tc.expect(t, attempts, err)
		})
	}
}

func TestAttemptsRun(t *testing.T) {
	transient := fmt.Errorf("%w: boom", ErrInternal)
	definitive := &BackendError{StatusCode: http.StatusNotFound}
	tests := []struct {
		name       string
		endpoints  []string
		maxRetries uint8
		failures   []error
		expect     func(t *testing.T, attempts []string, err error)
	}{
		{
			name:       "no endpoints",
			maxRetries: 3,
			expect: func(t *testing.T, attempts []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.Empty(attempts)
			},
		},
		{
			name:       "succeeds at once",
			endpoints:  []string{"a"},
			maxRetries: 3,
			expect: func(t *testing.T, attempts []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal([]string{"a"}, attempts)
			},
		},
		{
			name:       "retries transient failures on the next endpoint",
			endpoints:  []string{"a", "b"},
			maxRetries: 3,
			failures:   []error{transient, transient},
			expect: func(t *testing.T, attempts []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Len(attempts, 3)
				assert.NotEqual(attempts[0], attempts[1])
				assert.Equal(attempts[0], attempts[2])
			},
		},
		{
			name:       "wraps around the endpoints",
			endpoints:  []string{"a", "b", "c"},
			maxRetries: 3,
			failures:   []error{transient, transient, transient},
			expect: func(t *testing.T, attempts []string, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Len(attempts, 4)
				assert.ElementsMatch([]string{"a", "b", "c"}, attempts[:3])
				assert.Equal(attempts[0], attempts[3])
			},
		},
		{
			name:       "stops at a definitive failure",
			endpoints:  []string{"a"},
			maxRetries: 3,
			failures:   []error{definitive},
			expect: func(t *testing.T, attempts []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, definitive)
				assert.Len(attempts, 1)
			},
		},
		{
			name:       "exhausts the retries",
			endpoints:  []string{"a", "b"},
			maxRetries: 2,
			failures:   []error{transient, transient, transient},
			expect: func(t *testing.T, attempts []string, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, transient)
				assert.Len(attempts, 3)
				assert.NotEqual(attempts[0], attempts[1])
				assert.Equal(attempts[0], attempts[2])
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tried []string
			attempts, err := retryPolicy{maxRetries: tc.maxRetries}.newAttempts(tc.endpoints, time.Second)
			if err != nil {
				tc.expect(t, tried, err)
				return
			}

			resp, cancel, err := attempts.run(context.Background(), func(_ context.Context, endpoint string) (*http.Response, error) {
				tried = append(tried, endpoint)
				if len(tried) <= len(tc.failures) {
					return nil, tc.failures[len(tried)-1]
				}

				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			})
			if err == nil {
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				cancel()
			}
			tc.expect(t, tried, err)
		})
	}
}

func TestAttemptsShareTheRetriesAcrossRunsAndBackoffs(t *testing.T) {
	transient := fmt.Errorf("%w: boom", ErrInternal)
	tests := []struct {
		name       string
		maxRetries uint8
		failures   []error
		expect     func(t *testing.T, tried []string, ok bool, err error)
	}{
		{
			name:       "a retry left after the backoff",
			maxRetries: 2,
			failures:   []error{transient},
			expect: func(t *testing.T, tried []string, ok bool, err error) {
				assert := assert.New(t)
				assert.True(ok)
				assert.NoError(err)
				assert.Len(tried, 3)
				assert.NotEqual(tried[0], tried[1])
				assert.Equal(tried[0], tried[2])
			},
		},
		{
			name:       "no retry left for the backoff",
			maxRetries: 1,
			failures:   []error{transient},
			expect: func(t *testing.T, tried []string, ok bool, err error) {
				assert := assert.New(t)
				assert.False(ok)
				assert.NoError(err)
				assert.Len(tried, 2)
			},
		},
		{
			name:       "no retry left after the backoff",
			maxRetries: 1,
			failures:   []error{nil, transient},
			expect: func(t *testing.T, tried []string, ok bool, err error) {
				assert := assert.New(t)
				assert.True(ok)
				assert.ErrorIs(err, transient)
				assert.Len(tried, 2)
				assert.NotEqual(tried[0], tried[1])
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tried []string
			send := func(_ context.Context, endpoint string) (*http.Response, error) {
				tried = append(tried, endpoint)
				if len(tried) <= len(tc.failures) && tc.failures[len(tried)-1] != nil {
					return nil, tc.failures[len(tried)-1]
				}

				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			}

			attempts, err := retryPolicy{maxRetries: tc.maxRetries}.newAttempts([]string{"a", "b"}, time.Second)
			assert.NoError(t, err)

			_, cancel, err := attempts.run(context.Background(), send)
			assert.NoError(t, err)
			cancel()

			_, ok := attempts.backoff()
			if ok {
				_, cancel, err = attempts.run(context.Background(), send)
				if err == nil {
					cancel()
				}
			}
			tc.expect(t, tried, ok, err)
		})
	}
}

func TestAttemptsSleepStopsWhenTheContextEnds(t *testing.T) {
	other := errors.New("other")
	tests := []struct {
		name   string
		cause  error
		expect func(t *testing.T, err error)
	}{
		{
			name: "not ended",
			expect: func(t *testing.T, err error) {
				assert.NoError(t, err)
			},
		},
		{
			name:  "cancelled",
			cause: context.Canceled,
			expect: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrInternal)
			},
		},
		{
			name:  "deadline exceeded",
			cause: context.DeadlineExceeded,
			expect: func(t *testing.T, err error) {
				assert.ErrorIs(t, err, ErrRequestTimeout)
			},
		},
		{
			name:  "another cause",
			cause: other,
			expect: func(t *testing.T, err error) {
				assert.Equal(t, other, err)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)

			if tc.cause != nil {
				cancel(tc.cause)
			}
			attempts, err := retryPolicy{}.newAttempts([]string{"a"}, time.Second)
			assert.NoError(t, err)

			tc.expect(t, attempts.sleep(ctx, 0))
		})
	}
}
