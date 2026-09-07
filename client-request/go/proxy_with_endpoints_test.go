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
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewWithEndpoints(t *testing.T) {
	assert := assert.New(t)

	endpoints := []string{"http://127.0.0.1:4001", "http://127.0.0.1:4001", "http://127.0.0.1:4002"}
	p, err := NewWithEndpoints(endpoints)
	assert.NoError(err)

	// Each distinct endpoint gets its own client with a reusable connection
	// pool, deduplicated across the given endpoints.
	assert.Equal(uint8(defaultMaxRetries), p.retry.maxRetries)
	assert.Equal(endpoints, p.endpoints)
	assert.Len(p.clients, 2)
	assert.NotNil(p.clients["http://127.0.0.1:4001"])
	assert.NotNil(p.clients["http://127.0.0.1:4002"])
}

func TestNewWithEndpointsEmpty(t *testing.T) {
	assert := assert.New(t)

	_, err := NewWithEndpoints(nil)
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestNewWithEndpointsInvalidMaxRetries(t *testing.T) {
	assert := assert.New(t)

	_, err := NewWithEndpoints([]string{"http://127.0.0.1:4001"}, WithProxyWithEndpointsMaxRetries(11))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestNewWithEndpointsInvalidEndpoint(t *testing.T) {
	assert := assert.New(t)

	_, err := NewWithEndpoints([]string{"://"})
	assert.ErrorIs(err, ErrInternal)
}

func TestGetWithEndpoints(t *testing.T) {
	assert := assert.New(t)

	goodProxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Use-P2P", r.Header.Get("X-Dragonfly-Use-P2P"))
		w.Header().Set("X-Echo-Tag", r.Header.Get("X-Dragonfly-Tag"))
		w.Header().Set("X-Echo-Application", r.Header.Get("X-Dragonfly-Application"))
		fmt.Fprint(w, "ok")
	})

	// The request is scattered across the endpoints, retrying on the
	// unreachable one until the good one serves it.
	endpoints := []string{"http://127.0.0.1:1", fmt.Sprintf("http://127.0.0.1:%d", goodProxyPort)}
	proxy, err := NewWithEndpoints(endpoints)
	assert.NoError(err)

	req := NewGetRequest(
		"http://example.com/file.txt",
		WithGetRequestTag("tag1"),
		WithGetRequestApplication("app1"),
	)

	resp, err := proxy.Get(context.Background(), req)
	assert.NoError(err)
	defer resp.Body.Close()

	assert.True(resp.Success)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Equal("true", resp.Header.Get("X-Echo-Use-P2P"))
	assert.Equal("tag1", resp.Header.Get("X-Echo-Tag"))
	assert.Equal("app1", resp.Header.Get("X-Echo-Application"))

	body, err := io.ReadAll(resp.Body)
	assert.NoError(err)
	assert.Equal("ok", string(body))
}

func TestGetIntoWithEndpoints(t *testing.T) {
	assert := assert.New(t)

	goodProxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})

	endpoints := []string{"http://127.0.0.1:1", fmt.Sprintf("http://127.0.0.1:%d", goodProxyPort)}
	proxy, err := NewWithEndpoints(endpoints)
	assert.NoError(err)

	var buf bytes.Buffer
	resp, err := proxy.GetInto(context.Background(), NewGetRequest("http://example.com/file.txt"), &buf)
	assert.NoError(err)

	assert.True(resp.Success)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Nil(resp.Body)
	assert.Equal("ok", buf.String())
}

func TestGetWithEndpointsAllEndpointsDown(t *testing.T) {
	assert := assert.New(t)

	proxy, err := NewWithEndpoints([]string{"http://127.0.0.1:1"})
	assert.NoError(err)

	_, err = proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt"))
	assert.ErrorIs(err, ErrInternal)
}

func TestGetWithEndpointsInvalidReplicas(t *testing.T) {
	assert := assert.New(t)

	proxy, err := NewWithEndpoints([]string{"http://127.0.0.1:4001"})
	assert.NoError(err)

	_, err = proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(0)))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestGetWithEndpointsErrorTypes(t *testing.T) {
	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if errorType := r.Header.Get("X-Dragonfly-Tag"); errorType != "" {
			w.Header().Set("X-Dragonfly-Error-Type", errorType)
		}

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})

	proxy, err := NewWithEndpoints([]string{fmt.Sprintf("http://127.0.0.1:%d", proxyPort)})
	assert.NoError(t, err)

	tests := []struct {
		name   string
		tag    string
		expect func(t *testing.T, err error)
	}{
		{
			name: "backend",
			tag:  "backend",
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal("boom", backendErr.Message)
				assert.Equal(http.StatusInternalServerError, backendErr.StatusCode)
			},
		},
		{
			name: "proxy",
			tag:  "proxy",
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal("boom", proxyErr.Message)
				assert.Equal(http.StatusInternalServerError, proxyErr.StatusCode)
			},
		},
		{
			name: "dfdaemon",
			tag:  "dfdaemon",
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				var dfdaemonErr *DfdaemonError
				assert.ErrorAs(err, &dfdaemonErr)
				assert.Equal("boom", dfdaemonErr.Message)
				assert.Equal(http.StatusInternalServerError, dfdaemonErr.StatusCode)
			},
		},
		{
			name: "unknown status code",
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Contains(proxyErr.Message, "unexpected status code")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var opts []GetRequestOption
			if tc.tag != "" {
				opts = append(opts, WithGetRequestTag(tc.tag))
			}

			_, err := proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", opts...))
			tc.expect(t, err)
		})
	}
}

func TestGetWithEndpointsRetriesOnlyTransientAnswers(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		errorType  string
		maxRetries uint8
		expect     func(t *testing.T, hits int32, err error)
	}{
		{
			name:       "503 backend",
			status:     http.StatusServiceUnavailable,
			errorType:  "backend",
			maxRetries: 2,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusServiceUnavailable, backendErr.StatusCode)
				assert.Equal(int32(3), hits)
			},
		},
		{
			name:       "503 proxy",
			status:     http.StatusServiceUnavailable,
			errorType:  "proxy",
			maxRetries: 2,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusServiceUnavailable, proxyErr.StatusCode)
				assert.Equal(int32(3), hits)
			},
		},
		{
			name:       "500 dfdaemon",
			status:     http.StatusInternalServerError,
			errorType:  "dfdaemon",
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var dfdaemonErr *DfdaemonError
				assert.ErrorAs(err, &dfdaemonErr)
				assert.Equal(http.StatusInternalServerError, dfdaemonErr.StatusCode)
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "507 dfdaemon",
			status:     http.StatusInsufficientStorage,
			errorType:  "dfdaemon",
			maxRetries: 2,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var dfdaemonErr *DfdaemonError
				assert.ErrorAs(err, &dfdaemonErr)
				assert.Equal(http.StatusInsufficientStorage, dfdaemonErr.StatusCode)
				assert.Equal(int32(3), hits)
			},
		},
		{
			name:       "422 dfdaemon",
			status:     http.StatusUnprocessableEntity,
			errorType:  "dfdaemon",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var dfdaemonErr *DfdaemonError
				assert.ErrorAs(err, &dfdaemonErr)
				assert.Equal(http.StatusUnprocessableEntity, dfdaemonErr.StatusCode)
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "408 proxy",
			status:     http.StatusRequestTimeout,
			errorType:  "proxy",
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusRequestTimeout, proxyErr.StatusCode)
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "429 backend",
			status:     http.StatusTooManyRequests,
			errorType:  "backend",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusTooManyRequests, backendErr.StatusCode)
				assert.Equal(int32(4), hits)
			},
		},
		{
			name:       "429 proxy",
			status:     http.StatusTooManyRequests,
			errorType:  "proxy",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusTooManyRequests, proxyErr.StatusCode)
				assert.Equal(int32(4), hits)
			},
		},
		{
			name:       "429 proxy without retries",
			status:     http.StatusTooManyRequests,
			errorType:  "proxy",
			maxRetries: 0,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusTooManyRequests, proxyErr.StatusCode)
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "401 proxy",
			status:     http.StatusUnauthorized,
			errorType:  "proxy",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusUnauthorized, proxyErr.StatusCode)
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "403 proxy",
			status:     http.StatusForbidden,
			errorType:  "proxy",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusForbidden, proxyErr.StatusCode)
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "404 backend",
			status:     http.StatusNotFound,
			errorType:  "backend",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusNotFound, backendErr.StatusCode)
				assert.Equal(int32(1), hits)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("X-Dragonfly-Error-Type", tc.errorType)
				w.WriteHeader(tc.status)
			})

			proxy, err := NewWithEndpoints([]string{fmt.Sprintf("http://127.0.0.1:%d", proxyPort)}, WithProxyWithEndpointsMaxRetries(tc.maxRetries))
			assert.NoError(t, err)

			_, err = proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt"))
			tc.expect(t, hits.Load(), err)
		})
	}
}

func TestGetIntoWithEndpointsDoesNotRetryATruncatedBody(t *testing.T) {
	assert := assert.New(t)

	var hits atomic.Int32
	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Length", "15")
		fmt.Fprint(w, "hello")
	})

	proxy, err := NewWithEndpoints([]string{fmt.Sprintf("http://127.0.0.1:%d", proxyPort)}, WithProxyWithEndpointsMaxRetries(3))
	assert.NoError(err)

	var buf bytes.Buffer
	_, err = proxy.GetInto(context.Background(), NewGetRequest("http://example.com/file.txt"), &buf)
	assert.ErrorIs(err, ErrInternal)
	assert.Equal(int32(1), hits.Load())
}

func TestGetIntoWithEndpointsTimeout(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	})

	proxy, err := NewWithEndpoints([]string{fmt.Sprintf("http://127.0.0.1:%d", proxyPort)})
	assert.NoError(err)

	var buf bytes.Buffer
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestTimeout(50*time.Millisecond))
	_, err = proxy.GetInto(context.Background(), req, &buf)
	assert.ErrorIs(err, ErrRequestTimeout)
}
