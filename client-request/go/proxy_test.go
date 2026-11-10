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
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	"d7y.io/dragonfly/v2/pkg/idgen"
	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"d7y.io/dragonfly-sdk/client-request/go/internal/selector"
)

func TestNewSuccess(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	p, err := New(context.Background(), endpoint)
	assert.NoError(err)
	assert.Equal(uint8(1), p.retry.maxRetries)
	p.Close()
}

func TestNewEmptyEndpoint(t *testing.T) {
	assert := assert.New(t)
	_, err := New(context.Background(), "")
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestNewInvalidRetryTimes(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	_, err := New(context.Background(), endpoint, WithProxyMaxRetries(11))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestNewInvalidHealthCheckInterval(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	_, err := New(context.Background(), endpoint, WithProxyHealthCheckInterval(0))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestGet(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Echo-Use-P2P", r.Header.Get("X-Dragonfly-Use-P2P"))
		w.Header().Set("X-Echo-Tag", r.Header.Get("X-Dragonfly-Tag"))
		w.Header().Set("X-Echo-Application", r.Header.Get("X-Dragonfly-Application"))
		fmt.Fprint(w, "hello dragonfly")
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewGetRequest(
		"http://example.com/file.txt",
		WithGetRequestReplicas(1),
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
	assert.Equal("hello dragonfly", string(body))
}

func TestGetInto(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello dragonfly")
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	var buf bytes.Buffer
	resp, err := proxy.GetInto(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1)), &buf)
	assert.NoError(err)

	assert.True(resp.Success)
	assert.Equal(http.StatusOK, resp.StatusCode)
	assert.Nil(resp.Body)
	assert.Equal("hello dragonfly", buf.String())
}

func TestSendToIgnoresFailuresOtherThanConnecting(t *testing.T) {
	refusingProxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})

	resettingProxy, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(t, err)
	defer resettingProxy.Close()
	go func() {
		for {
			conn, err := resettingProxy.Accept()
			if err != nil {
				return
			}

			_ = conn.(*net.TCPConn).SetLinger(0)
			conn.Close()
		}
	}()
	resettingProxyPort := int32(resettingProxy.Addr().(*net.TCPAddr).Port)

	tests := []struct {
		name      string
		proxyPort int32
		url       string
	}{
		{
			name:      "tunnel refused",
			proxyPort: refusingProxyPort,
			url:       "https://example.com/file.txt",
		},
		{
			name:      "connection reset",
			proxyPort: resettingProxyPort,
			url:       "http://example.com/file.txt",
		},
		{
			name:      "tunnel reset",
			proxyPort: resettingProxyPort,
			url:       "https://example.com/file.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			host := createSeedPeerHost("seed-peer-1", 0, tc.proxyPort)
			proxy, err := NewWithSeedPeers([]SeedPeer{{Host: host, Healthy: true}})
			assert.NoError(err)

			endpoint := proxy.proxy.seedPeerSelector.ProxyEndpoint(host)
			req := NewGetRequest(tc.url, WithGetRequestReplicas(1))
			for range selector.MaxFailedCount {
				_, err := proxy.proxy.sendTo(context.Background(), endpoint, req)
				assert.ErrorIs(err, ErrInternal)
			}

			assert.False(proxy.proxy.seedPeerSelector.IsEjected(endpoint))
		})
	}
}

func TestEjectionTransportCountsOnlyFailuresToConnect(t *testing.T) {
	tests := []struct {
		name    string
		err     error
		ejected bool
	}{
		{
			name:    "connection refused",
			err:     &net.OpError{Op: "proxyconnect", Net: "tcp", Err: syscall.ECONNREFUSED},
			ejected: true,
		},
		{
			name:    "host unreachable",
			err:     &net.OpError{Op: "proxyconnect", Net: "tcp", Err: syscall.EHOSTUNREACH},
			ejected: true,
		},
		{
			name:    "cancelled dial",
			err:     &net.OpError{Op: "proxyconnect", Net: "tcp", Err: context.Canceled},
			ejected: false,
		},
		{
			name:    "timeout to connect",
			err:     &net.OpError{Op: "proxyconnect", Net: "tcp", Err: os.ErrDeadlineExceeded},
			ejected: false,
		},
		{
			name:    "too many open files",
			err:     &net.OpError{Op: "proxyconnect", Net: "tcp", Err: syscall.EMFILE},
			ejected: false,
		},
		{
			name:    "reset connection",
			err:     &net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET},
			ejected: false,
		},
		{
			name:    "refused tunnel",
			err:     errors.New("Bad Gateway"),
			ejected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			endpoint := "http://127.0.0.1:4001"
			s := selector.New()
			transport := &ejectionTransport{
				transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
					return nil, tc.err
				}),
				seedPeerSelector: s,
				endpoint:         endpoint,
			}

			req, err := http.NewRequest(http.MethodGet, "http://example.com/file.txt", nil)
			assert.NoError(err)
			for range selector.MaxFailedCount {
				_, err := transport.RoundTrip(req)
				assert.Error(err)
			}

			assert.Equal(tc.ejected, s.IsEjected(endpoint))
		})
	}
}

func TestEjectionTransportClosesIdleConnections(t *testing.T) {
	assert := assert.New(t)
	transport := &idleConnectionsCloser{}
	client := &http.Client{Transport: &ejectionTransport{
		transport:        transport,
		seedPeerSelector: selector.New(),
		endpoint:         "http://127.0.0.1:4001",
	}}

	client.CloseIdleConnections()
	assert.True(transport.closed)
}

func TestGetFailsFastOnceItsProxyIsEjected(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{
			name: "http",
			url:  "http://example.com/file.txt",
		},
		{
			name: "https",
			url:  "https://example.com/file.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			host := createSeedPeerHost("seed-peer-1", 0, 1)
			proxy, err := NewWithSeedPeers([]SeedPeer{{Host: host, Healthy: true}}, WithProxyWithSeedPeersMaxRetries(0))
			assert.NoError(err)

			req := NewGetRequest(tc.url, WithGetRequestReplicas(1))
			for range selector.MaxFailedCount {
				_, err := proxy.Get(context.Background(), req)
				assert.Error(err)
			}

			_, err = proxy.Get(context.Background(), req)
			assert.ErrorIs(err, ErrInternal)
			assert.ErrorContains(err, "ejected")
		})
	}
}

func TestLookupEndpointsSkipsTheEjectedReplicas(t *testing.T) {
	assert := assert.New(t)
	proxy, err := NewWithSeedPeers([]SeedPeer{
		{Host: createSeedPeerHost("seed-peer-1", 0, 4001), Healthy: true},
		{Host: createSeedPeerHost("seed-peer-2", 0, 4002), Healthy: true},
	})
	assert.NoError(err)

	req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(2))
	endpoints, err := proxy.LookupEndpoints(context.Background(), req)
	assert.NoError(err)
	for range selector.MaxFailedCount {
		proxy.proxy.seedPeerSelector.OnFailure(endpoints[0])
	}

	ejectedEndpoints, err := proxy.LookupEndpoints(context.Background(), req)
	assert.NoError(err)
	assert.Equal(endpoints[1:], ejectedEndpoints)
}

func TestSendToClearsTheFailuresOfAProxyThatAnswers(t *testing.T) {
	tests := []struct {
		name   string
		status int
	}{
		{
			name:   "not found",
			status: http.StatusNotFound,
		},
		{
			name:   "internal server error",
			status: http.StatusInternalServerError,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
			})

			host := createSeedPeerHost("seed-peer-1", 0, proxyPort)
			proxy, err := NewWithSeedPeers([]SeedPeer{{Host: host, Healthy: true}})
			assert.NoError(err)

			endpoint := proxy.proxy.seedPeerSelector.ProxyEndpoint(host)
			for range selector.MaxFailedCount - 1 {
				proxy.proxy.seedPeerSelector.OnFailure(endpoint)
			}

			req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1))
			_, err = proxy.proxy.sendTo(context.Background(), endpoint, req)
			assert.Error(err)

			proxy.proxy.seedPeerSelector.OnFailure(endpoint)
			assert.False(proxy.proxy.seedPeerSelector.IsEjected(endpoint))
		})
	}
}

func TestSchedulerClientListSeedPeers(t *testing.T) {
	assert := assert.New(t)

	port := setupMockSeedPeer(t, nil)

	notServingListener, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(err)
	notServing := grpc.NewServer()
	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	healthpb.RegisterHealthServer(notServing, healthServer)
	go func() { _ = notServing.Serve(notServingListener) }()
	defer notServing.Stop()

	endpoint := setupMockScheduler(t, []*commonv2.Host{
		createSeedPeerHost("seed-peer-1", port, 0),
		createSeedPeerHost("seed-peer-2", 1, 0),
		createSeedPeerHost("seed-peer-3", int32(notServingListener.Addr().(*net.TCPAddr).Port), 0),
	})

	schedulerClient, err := NewSchedulerClient(endpoint, 5*time.Second)
	assert.NoError(err)
	defer schedulerClient.Close()

	seedPeers, err := schedulerClient.ListSeedPeers(context.Background())
	assert.NoError(err)

	healthy := make(map[string]bool, len(seedPeers))
	for _, seedPeer := range seedPeers {
		healthy[seedPeer.Host.Name] = seedPeer.Healthy
	}
	assert.Equal(map[string]bool{"seed-peer-1": true, "seed-peer-2": false, "seed-peer-3": false}, healthy)
}

func TestSchedulerClientListSeedPeersWhenTheContextEnds(t *testing.T) {
	assert := assert.New(t)
	hanging, err := net.Listen("tcp", "127.0.0.1:0")
	assert.NoError(err)
	defer hanging.Close()
	endpoint := setupMockScheduler(t, []*commonv2.Host{
		createSeedPeerHost("seed-peer-1", int32(hanging.Addr().(*net.TCPAddr).Port), 0),
	})

	schedulerClient, err := NewSchedulerClient(endpoint, 5*time.Second)
	assert.NoError(err)
	defer schedulerClient.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	_, err = schedulerClient.ListSeedPeers(ctx)
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorIs(err, context.DeadlineExceeded)
}

func TestSchedulerClientListSeedPeersFromAnUnreachableScheduler(t *testing.T) {
	assert := assert.New(t)
	schedulerClient, err := NewSchedulerClient("http://127.0.0.1:1", time.Second)
	assert.NoError(err)
	defer schedulerClient.Close()

	_, err = schedulerClient.ListSeedPeers(context.Background())
	assert.ErrorIs(err, ErrInternal)
}

func TestGetErrorTypes(t *testing.T) {
	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if errorType := r.Header.Get("X-Dragonfly-Tag"); errorType != "" {
			w.Header().Set("X-Dragonfly-Error-Type", errorType)
		}

		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "boom")
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(t, err)
	defer proxy.Close()

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
			opts := []GetRequestOption{WithGetRequestReplicas(1)}
			if tc.tag != "" {
				opts = append(opts, WithGetRequestTag(tc.tag))
			}

			_, err := proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", opts...))
			tc.expect(t, err)
		})
	}
}

func TestGetScattersAcrossReplicas(t *testing.T) {
	assert := assert.New(t)

	badProxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	goodProxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ok")
	})
	endpoint := setupMockScheduler(t, []*commonv2.Host{
		createSeedPeerHost("seed-peer-1", setupMockSeedPeer(t, nil), badProxyPort),
		createSeedPeerHost("seed-peer-2", setupMockSeedPeer(t, nil), goodProxyPort),
	})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	resp, err := proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(2)))
	assert.NoError(err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(err)
	assert.Equal("ok", string(body))
}

func TestGetResumesABodyThatBreaksOff(t *testing.T) {
	tests := []struct {
		name       string
		stall      bool
		timeout    time.Duration
		maxRetries uint8
		expect     func(t *testing.T, content []byte, hits int32, err error)
	}{
		{
			name:       "truncated body",
			timeout:    time.Second,
			maxRetries: 1,
			expect: func(t *testing.T, content []byte, hits int32, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "stalled body",
			stall:      true,
			timeout:    200 * time.Millisecond,
			maxRetries: 1,
			expect: func(t *testing.T, content []byte, hits int32, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal("hello dragonfly", string(content))
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "truncated body without retries",
			timeout:    time.Second,
			maxRetries: 0,
			expect: func(t *testing.T, content []byte, hits int32, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInternal)
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "stalled body without retries",
			stall:      true,
			timeout:    200 * time.Millisecond,
			maxRetries: 0,
			expect: func(t *testing.T, content []byte, hits int32, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrRequestTimeout)
				assert.Equal(int32(1), hits)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
				if hits.Add(1) > 1 {
					http.ServeContent(w, r, "", time.Time{}, strings.NewReader("hello dragonfly"))
					return
				}

				w.Header().Set("Content-Length", "15")
				fmt.Fprint(w, "hello d")
				if tc.stall {
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			})
			port := setupMockSeedPeer(t, nil)
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

			proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(tc.maxRetries))
			assert.NoError(t, err)
			defer proxy.Close()

			resp, err := proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1), WithGetRequestTimeout(tc.timeout)))
			assert.NoError(t, err)
			defer resp.Body.Close()

			content, err := io.ReadAll(resp.Body)
			tc.expect(t, content, hits.Load(), err)
		})
	}
}

func TestGetRetriesOnlyTransientAnswers(t *testing.T) {
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
			name:       "500 backend",
			status:     http.StatusInternalServerError,
			errorType:  "backend",
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusInternalServerError, backendErr.StatusCode)
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "500 proxy",
			status:     http.StatusInternalServerError,
			errorType:  "proxy",
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusInternalServerError, proxyErr.StatusCode)
				assert.Equal(int32(2), hits)
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
			name:       "408 backend",
			status:     http.StatusRequestTimeout,
			errorType:  "backend",
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusRequestTimeout, backendErr.StatusCode)
				assert.Equal(int32(2), hits)
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
			name:       "401 backend",
			status:     http.StatusUnauthorized,
			errorType:  "backend",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusUnauthorized, backendErr.StatusCode)
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
			name:       "403 backend",
			status:     http.StatusForbidden,
			errorType:  "backend",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var backendErr *BackendError
				assert.ErrorAs(err, &backendErr)
				assert.Equal(http.StatusForbidden, backendErr.StatusCode)
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
		{
			name:       "404 proxy",
			status:     http.StatusNotFound,
			errorType:  "proxy",
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				var proxyErr *ProxyError
				assert.ErrorAs(err, &proxyErr)
				assert.Equal(http.StatusNotFound, proxyErr.StatusCode)
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
			port := setupMockSeedPeer(t, nil)
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

			exponential := &backoff.ExponentialBackOff{InitialInterval: time.Millisecond, RandomizationFactor: 0.5, Multiplier: 2, MaxInterval: 2 * time.Millisecond}
			proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(tc.maxRetries), WithProxyBackoff(exponential))
			assert.NoError(t, err)
			defer proxy.Close()

			_, err = proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1)))
			tc.expect(t, hits.Load(), err)
		})
	}
}

func TestTimeoutAppliesToEachAttempt(t *testing.T) {
	assert := assert.New(t)

	var hits atomic.Int32
	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		if hits.Add(1) == 1 {
			<-r.Context().Done()
			return
		}

		fmt.Fprint(w, "ok")
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	timeout := 300 * time.Millisecond
	start := time.Now()
	var buf bytes.Buffer
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1), WithGetRequestTimeout(timeout))
	_, err = proxy.GetInto(context.Background(), req, &buf)
	assert.NoError(err)
	assert.Equal("ok", buf.String())
	assert.GreaterOrEqual(time.Since(start), timeout)
	assert.Less(time.Since(start), 3*timeout)
}

func TestGetInvalidReplicas(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	_, err = proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(0)))
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestGetInvalidURL(t *testing.T) {
	endpoint := setupMockScheduler(t, nil)

	tests := []struct {
		name string
		url  string
	}{
		{
			name: "file scheme",
			url:  "file:///tmp/file.txt",
		},
		{
			name: "ftp scheme",
			url:  "ftp://example.com/file.txt",
		},
		{
			name: "no scheme",
			url:  "example.com/file.txt",
		},
		{
			name: "empty hostname",
			url:  "http://:80/file.txt",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			proxy, err := New(context.Background(), endpoint)
			assert.NoError(err)
			defer proxy.Close()

			_, err = proxy.Get(context.Background(), NewGetRequest(tc.url))
			assert.ErrorIs(err, ErrInvalidArgument)
		})
	}
}

func TestGetInvalidHeader(t *testing.T) {
	tests := []struct {
		name string
		opt  GetRequestOption
	}{
		{
			name: "invalid tag",
			opt:  WithGetRequestTag("bad\r\nvalue"),
		},
		{
			name: "invalid application",
			opt:  WithGetRequestApplication("bad\r\nvalue"),
		},
		{
			name: "invalid header name",
			opt:  WithGetRequestHeader(http.Header{"Bad Name": {"value"}}),
		},
	}

	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", setupMockSeedPeer(t, nil), 1)})
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			proxy, err := New(context.Background(), endpoint)
			assert.NoError(err)
			defer proxy.Close()

			_, err = proxy.Get(context.Background(), NewGetRequest("http://example.com/file.txt", tc.opt))
			assert.ErrorIs(err, ErrInvalidArgument)
		})
	}
}

func TestGetIntoTimeout(t *testing.T) {
	assert := assert.New(t)

	proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	})
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	var buf bytes.Buffer
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1), WithGetRequestTimeout(50*time.Millisecond))
	_, err = proxy.GetInto(context.Background(), req, &buf)
	assert.ErrorIs(err, ErrRequestTimeout)
}

func TestLookupEndpoints(t *testing.T) {
	assert := assert.New(t)
	endpoints := make(map[string]string, 3)
	var hosts []*commonv2.Host
	for i, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		proxyPort := int32(4001 + i)
		endpoints[name] = fmt.Sprintf("http://127.0.0.1:%d", proxyPort)
		hosts = append(hosts, createSeedPeerHost(name, setupMockSeedPeer(t, nil), proxyPort))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	blobURL := "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
	tests := []struct {
		req   *GetRequest
		names []string
	}{
		{
			req: NewGetRequest(
				"https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar",
				WithGetRequestPieceLength(4194304),
				WithGetRequestTag("tag-a"),
				WithGetRequestApplication("app-a"),
				WithGetRequestFilteredQueryParams([]string{"Expires", "Signature"}),
				WithGetRequestReplicas(3),
			),
			names: []string{"seed-peer-3", "seed-peer-2", "seed-peer-1"},
		},
		{
			req: NewGetRequest(
				"https://example.com/file.txt?Expires=e2&Signature=s2&foo=bar",
				WithGetRequestPieceLength(4194304),
				WithGetRequestTag("tag-a"),
				WithGetRequestApplication("app-a"),
				WithGetRequestFilteredQueryParams([]string{"Expires", "Signature"}),
				WithGetRequestReplicas(3),
			),
			names: []string{"seed-peer-3", "seed-peer-2", "seed-peer-1"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt"),
			names: []string{"seed-peer-1", "seed-peer-2"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestReplicas(1)),
			names: []string{"seed-peer-1"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestTag("tag-a")),
			names: []string{"seed-peer-3", "seed-peer-2"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestTag("tag-b")),
			names: []string{"seed-peer-2", "seed-peer-1"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt", WithGetRequestApplication("app-a")),
			names: []string{"seed-peer-1", "seed-peer-3"},
		},
		{
			req: NewGetRequest(
				"https://example.com/file.txt",
				WithGetRequestContentForCalculatingTaskID("This is a test file"),
				WithGetRequestReplicas(3),
			),
			names: []string{"seed-peer-2", "seed-peer-3", "seed-peer-1"},
		},
		{
			req:   NewGetRequest(blobURL, WithGetRequestReplicas(3)),
			names: []string{"seed-peer-3", "seed-peer-2", "seed-peer-1"},
		},
		{
			req:   NewGetRequest(blobURL, WithGetRequestEnableTaskIDBasedBlobDigest(false)),
			names: []string{"seed-peer-3", "seed-peer-1"},
		},
	}

	for _, tt := range tests {
		expected := make([]string, 0, len(tt.names))
		for _, name := range tt.names {
			expected = append(expected, endpoints[name])
		}

		addrs, err := proxy.LookupEndpoints(context.Background(), tt.req)
		assert.NoError(err)
		assert.Equal(expected, addrs)
	}
}

func TestLookupEndpointsNoAvailableSeedPeers(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	_, err = proxy.LookupEndpoints(context.Background(), NewGetRequest("http://example.com/payload.txt"))
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorContains(err, "failed to select seed peers")
}

func TestTaskIDBlobDigestDisabled(t *testing.T) {
	assert := assert.New(t)

	blobURL := "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
	id, err := idgen.TaskIDV2(blobURL, nil, "", "", nil, "", false)
	assert.NoError(err)
	assert.NotEqual("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", id)
}
