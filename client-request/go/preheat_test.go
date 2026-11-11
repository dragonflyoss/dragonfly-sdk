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
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	"d7y.io/dragonfly/v2/pkg/net/ip"
	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPreheatNoAvailableSeedPeers(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestTag("preheat"), WithPreheatRequestApplication("dfctl"))

	err = proxy.Preheat(context.Background(), req)
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorContains(err, "failed to select seed peers")
}

func TestPreheatSucceedsWithSeedPeer(t *testing.T) {
	assert := assert.New(t)
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestTag("preheat"), WithPreheatRequestApplication("dfctl"), WithPreheatRequestReplicas(1))

	assert.NoError(proxy.Preheat(context.Background(), req))
}

func TestPreheatInsufficientSeedPeers(t *testing.T) {
	assert := assert.New(t)
	port := setupMockSeedPeer(t, nil)
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt")
	err = proxy.Preheat(context.Background(), req)
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorContains(err, "insufficient seed peers")
}

func TestPreheatAllSeedPeers(t *testing.T) {
	assert := assert.New(t)

	var mu sync.Mutex
	preheated := make(map[string]bool)
	var hosts []*commonv2.Host
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		port := setupMockSeedPeerServer(t, &mockSeedPeer{onDownloadTask: func() {
			mu.Lock()
			preheated[name] = true
			mu.Unlock()
		}})
		hosts = append(hosts, createSeedPeerHost(name, port, 0))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestReplicas(5), WithPreheatRequestScope(ScopeAllSeedPeers))
	assert.NoError(proxy.Preheat(context.Background(), req))

	mu.Lock()
	assert.Len(preheated, len(hosts))
	mu.Unlock()
}

func TestPreheatInvalidScope(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestScope(Scope("all_peers")))
	err = proxy.Preheat(context.Background(), req)
	assert.ErrorIs(err, ErrInvalidArgument)
	assert.ErrorContains(err, "invalid scope")
}

func TestPreheatFailsWhenSeedPeerDownloadFails(t *testing.T) {
	assert := assert.New(t)
	port := setupMockSeedPeer(t, status.Error(codes.Internal, "storage is full"))
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestTag("preheat"), WithPreheatRequestApplication("dfctl"), WithPreheatRequestReplicas(1))

	err = proxy.Preheat(context.Background(), req)
	assert.ErrorContains(err, "failed to download task")
}

func TestPreheatRetriesOnTheSameSeedPeer(t *testing.T) {
	assert := assert.New(t)

	var failingHits, healthyHits atomic.Int32
	failingPort := setupMockSeedPeerServer(t, &mockSeedPeer{
		downloadTaskErr: status.Error(codes.Unavailable, "seed peer is busy"),
		onDownloadTask:  func() { failingHits.Add(1) },
	})
	healthyPort := setupMockSeedPeerServer(t, &mockSeedPeer{onDownloadTask: func() { healthyHits.Add(1) }})
	endpoint := setupMockScheduler(t, []*commonv2.Host{
		createSeedPeerHost("seed-peer-1", failingPort, 0),
		createSeedPeerHost("seed-peer-2", healthyPort, 0),
	})

	proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(2))
	assert.NoError(err)
	defer proxy.Close()

	err = proxy.Preheat(context.Background(), NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestReplicas(2)))
	assert.Equal(codes.Unavailable, status.Code(err))
	assert.Equal(int32(3), failingHits.Load())
	assert.Equal(int32(1), healthyHits.Load())
}

func TestPreheatRetriesOnlyTransientDownloadFailures(t *testing.T) {
	tests := []struct {
		name       string
		code       codes.Code
		maxRetries uint8
		expect     func(t *testing.T, hits int32, err error)
	}{
		{
			name:       "internal",
			code:       codes.Internal,
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.Equal(codes.Internal, status.Code(err))
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "unavailable",
			code:       codes.Unavailable,
			maxRetries: 2,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.Equal(codes.Unavailable, status.Code(err))
				assert.Equal(int32(3), hits)
			},
		},
		{
			name:       "deadline exceeded",
			code:       codes.DeadlineExceeded,
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.Equal(codes.DeadlineExceeded, status.Code(err))
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "resource exhausted",
			code:       codes.ResourceExhausted,
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.Equal(codes.ResourceExhausted, status.Code(err))
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "not found",
			code:       codes.NotFound,
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.Equal(codes.NotFound, status.Code(err))
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "permission denied",
			code:       codes.PermissionDenied,
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.Equal(codes.PermissionDenied, status.Code(err))
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "invalid argument",
			code:       codes.InvalidArgument,
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.Equal(codes.InvalidArgument, status.Code(err))
				assert.Equal(int32(1), hits)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			port := setupMockSeedPeerServer(t, &mockSeedPeer{
				downloadTaskErr: status.Error(tc.code, "download failed"),
				onDownloadTask:  func() { hits.Add(1) },
			})
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

			exponential := &backoff.ExponentialBackOff{InitialInterval: time.Millisecond, Multiplier: 2, MaxInterval: 2 * time.Millisecond}
			proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(tc.maxRetries), WithProxyBackoff(exponential))
			assert.NoError(t, err)
			defer proxy.Close()

			err = proxy.Preheat(context.Background(), NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestReplicas(1)))
			tc.expect(t, hits.Load(), err)
		})
	}
}

func TestPreheatImageInvalidArgument(t *testing.T) {
	tests := []struct {
		name   string
		req    *PreheatImageRequest
		expect func(t *testing.T, err error)
	}{
		{
			name: "invalid reference",
			req:  NewPreheatImageRequest("invalid image reference!!"),
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.ErrorContains(err, "invalid image reference")
			},
		},
		{
			name: "invalid platform",
			req:  NewPreheatImageRequest("docker.io/library/nginx:latest", WithPreheatImageRequestPlatform("linux-amd64")),
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.ErrorContains(err, "invalid platform format")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := setupMockScheduler(t, nil)

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(t, err)
			defer proxy.Close()

			err = proxy.PreheatImage(context.Background(), tc.req)
			tc.expect(t, err)
		})
	}
}

func TestPreheatImageDownloadsManifestsAndBlobs(t *testing.T) {
	amd64Manifest := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
		"config": {
			"mediaType": "application/vnd.docker.container.image.v1+json",
			"size": 7023,
			"digest": "sha256:b5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7"
		},
		"layers": [
			{
				"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
				"size": 32654,
				"digest": "sha256:e692418e4cbaf90ca69d05a66403747baa33ee08806650b51fab815ad7fc331f"
			}
		]
	}`
	arm64Manifest := `{
		"schemaVersion": 2,
		"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
		"config": {
			"mediaType": "application/vnd.docker.container.image.v1+json",
			"size": 7023,
			"digest": "sha256:c5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7"
		},
		"layers": [
			{
				"mediaType": "application/vnd.docker.image.rootfs.diff.tar.gzip",
				"size": 32654,
				"digest": "sha256:e692418e4cbaf90ca69d05a66403747baa33ee08806650b51fab815ad7fc331f"
			}
		]
	}`
	amd64ManifestDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(amd64Manifest)))
	arm64ManifestDigest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(arm64Manifest)))
	manifestList := fmt.Sprintf(`{
		"schemaVersion": 2,
		"mediaType": "application/vnd.docker.distribution.manifest.list.v2+json",
		"manifests": [
			{
				"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
				"size": %d,
				"digest": "%s",
				"platform": {
					"architecture": "amd64",
					"os": "linux"
				}
			},
			{
				"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
				"size": %d,
				"digest": "%s",
				"platform": {
					"architecture": "arm64",
					"os": "linux"
				}
			},
			{
				"mediaType": "application/vnd.docker.distribution.manifest.v2+json",
				"size": %d,
				"digest": "%s",
				"platform": {
					"architecture": "arm64",
					"os": "linux",
					"variant": "v8"
				}
			}
		]
	}`, len(amd64Manifest), amd64ManifestDigest, len(arm64Manifest), arm64ManifestDigest, len(arm64Manifest), arm64ManifestDigest)

	listener, err := net.Listen("tcp", net.JoinHostPort(ip.IPv4.String(), "0"))
	if err != nil {
		t.Fatal(err)
	}

	registry := listener.Addr().String()
	mux := http.NewServeMux()
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%q,service=\"registry\"", "https://"+registry+"/token"))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"token":"test-token"}`)
	})
	mux.HandleFunc("/v2/foo/bar/manifests/1.0", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.list.v2+json")
		fmt.Fprint(w, manifestList)
	})
	mux.HandleFunc("/v2/foo/bar/manifests/"+amd64ManifestDigest, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		fmt.Fprint(w, amd64Manifest)
	})
	mux.HandleFunc("/v2/foo/bar/manifests/"+arm64ManifestDigest, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.docker.distribution.manifest.v2+json")
		fmt.Fprint(w, arm64Manifest)
	})
	server := httptest.NewUnstartedServer(mux)
	server.Listener.Close()
	server.Listener = listener
	server.StartTLS()
	defer server.Close()

	tests := []struct {
		name   string
		req    *PreheatImageRequest
		expect func(t *testing.T, downloads []*commonv2.Download, err error)
	}{
		{
			name: "all platforms",
			req:  NewPreheatImageRequest(registry+"/foo/bar:1.0", WithPreheatImageRequestReplicas(1)),
			expect: func(t *testing.T, downloads []*commonv2.Download, err error) {
				assert := assert.New(t)
				assert.NoError(err)

				var urls []string
				for _, download := range downloads {
					assert.Equal(registry+"/foo/bar:1.0", download.GetImage().GetReference())
					urls = append(urls, download.GetImage().GetPlatform()+" "+download.GetUrl())
				}

				assert.ElementsMatch([]string{
					fmt.Sprintf("linux/amd64 https://%s/v2/foo/bar/manifests/%s", registry, amd64ManifestDigest),
					fmt.Sprintf("linux/amd64 https://%s/v2/foo/bar/blobs/sha256:b5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7", registry),
					fmt.Sprintf("linux/amd64 https://%s/v2/foo/bar/blobs/sha256:e692418e4cbaf90ca69d05a66403747baa33ee08806650b51fab815ad7fc331f", registry),
					fmt.Sprintf("linux/arm64 https://%s/v2/foo/bar/manifests/%s", registry, arm64ManifestDigest),
					fmt.Sprintf("linux/arm64 https://%s/v2/foo/bar/blobs/sha256:c5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7", registry),
					fmt.Sprintf("linux/arm64 https://%s/v2/foo/bar/blobs/sha256:e692418e4cbaf90ca69d05a66403747baa33ee08806650b51fab815ad7fc331f", registry),
				}, urls)
			},
		},
		{
			name: "platform",
			req:  NewPreheatImageRequest(registry+"/foo/bar:1.0", WithPreheatImageRequestPlatform("linux/arm64"), WithPreheatImageRequestReplicas(1)),
			expect: func(t *testing.T, downloads []*commonv2.Download, err error) {
				assert := assert.New(t)
				assert.NoError(err)

				var urls []string
				for _, download := range downloads {
					assert.Equal(registry+"/foo/bar:1.0", download.GetImage().GetReference())
					urls = append(urls, download.GetImage().GetPlatform()+" "+download.GetUrl())
				}

				assert.ElementsMatch([]string{
					fmt.Sprintf("linux/arm64 https://%s/v2/foo/bar/manifests/%s", registry, arm64ManifestDigest),
					fmt.Sprintf("linux/arm64 https://%s/v2/foo/bar/blobs/sha256:c5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7", registry),
					fmt.Sprintf("linux/arm64 https://%s/v2/foo/bar/blobs/sha256:e692418e4cbaf90ca69d05a66403747baa33ee08806650b51fab815ad7fc331f", registry),
				}, urls)
			},
		},
		{
			name: "manifest digest",
			req:  NewPreheatImageRequest(registry+"/foo/bar@"+amd64ManifestDigest, WithPreheatImageRequestReplicas(1)),
			expect: func(t *testing.T, downloads []*commonv2.Download, err error) {
				assert := assert.New(t)
				assert.NoError(err)

				var urls []string
				for _, download := range downloads {
					assert.Equal(registry+"/foo/bar@"+amd64ManifestDigest, download.GetImage().GetReference())
					assert.Nil(download.GetImage().Platform)
					urls = append(urls, download.GetUrl())
				}

				assert.ElementsMatch([]string{
					fmt.Sprintf("https://%s/v2/foo/bar/manifests/%s", registry, amd64ManifestDigest),
					fmt.Sprintf("https://%s/v2/foo/bar/blobs/sha256:b5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7", registry),
					fmt.Sprintf("https://%s/v2/foo/bar/blobs/sha256:e692418e4cbaf90ca69d05a66403747baa33ee08806650b51fab815ad7fc331f", registry),
				}, urls)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			downloadTaskRequests := make(chan *dfdaemonv2.DownloadTaskRequest, 8)
			port := setupMockSeedPeerServer(t, &mockSeedPeer{
				downloadTask: func(req *dfdaemonv2.DownloadTaskRequest) { downloadTaskRequests <- req },
			})
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(t, err)
			defer proxy.Close()

			err = proxy.PreheatImage(context.Background(), tc.req)
			close(downloadTaskRequests)

			var downloads []*commonv2.Download
			for downloadTaskRequest := range downloadTaskRequests {
				downloads = append(downloads, downloadTaskRequest.GetDownload())
			}
			tc.expect(t, downloads, err)
		})
	}
}

func TestPreheatImageUnreachableRegistry(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	// Port 1 is reserved and refuses connections, so pulling the manifest fails.
	req := NewPreheatImageRequest("127.0.0.1:1/library/nginx:latest", WithPreheatImageRequestTimeout(5*time.Second))

	err = proxy.PreheatImage(context.Background(), req)
	assert.Error(err)
}

// TestPreheatAndGetHitSameSeedPeers asserts the core invariant of the
// replicas design: the seed peers that a preheat writes to are exactly the
// seed peers that downloads of the same task scatter across.
func TestPreheatAndGetHitSameSeedPeers(t *testing.T) {
	var mu sync.Mutex
	preheated := make(map[string]bool)
	served := make(map[string]bool)

	var hosts []*commonv2.Host
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		port := setupMockSeedPeerServer(t, &mockSeedPeer{onDownloadTask: func() {
			mu.Lock()
			preheated[name] = true
			mu.Unlock()
		}})

		proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			served[name] = true
			mu.Unlock()
			w.WriteHeader(http.StatusInternalServerError)
		})

		hosts = append(hosts, createSeedPeerHost(name, port, proxyPort))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(2))
	assert.NoError(t, err)
	defer proxy.Close()

	tests := []struct {
		url      string
		replicas int
	}{
		{"http://example.com/replicas-1.txt", 1},
		{"http://example.com/replicas-2.txt", 2},
		{"http://example.com/replicas-3.txt", 3},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d replicas", tt.replicas), func(t *testing.T) {
			assert := assert.New(t)

			mu.Lock()
			clear(preheated)
			clear(served)
			mu.Unlock()
			assert.NoError(proxy.Preheat(context.Background(), NewPreheatRequest(tt.url, WithPreheatRequestReplicas(tt.replicas))))

			mu.Lock()
			assert.Len(preheated, tt.replicas)
			mu.Unlock()

			_, err := proxy.Get(context.Background(), NewGetRequest(tt.url, WithGetRequestReplicas(tt.replicas)))
			assert.Error(err)

			mu.Lock()
			assert.Equal(preheated, served)
			mu.Unlock()
		})
	}
}
