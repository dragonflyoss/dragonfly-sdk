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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeleteImageInvalidArgument(t *testing.T) {
	tests := []struct {
		name   string
		req    *DeleteImageRequest
		expect func(t *testing.T, err error)
	}{
		{
			name: "invalid reference",
			req:  NewDeleteImageRequest("invalid image reference!!"),
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.ErrorContains(err, "invalid image reference")
			},
		},
		{
			name: "invalid platform",
			req:  NewDeleteImageRequest("docker.io/library/nginx:latest", WithDeleteImageRequestPlatform("linux-amd64")),
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.ErrorContains(err, "invalid platform format")
			},
		},
		{
			name: "invalid concurrent peer count",
			req:  NewDeleteImageRequest("docker.io/library/nginx:latest", WithDeleteImageRequestConcurrentPeerCount(0)),
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.ErrorContains(err, "concurrent peer count must be positive")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			endpoint := setupMockScheduler(t, nil)

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(t, err)
			defer proxy.Close()

			_, err = proxy.DeleteImage(context.Background(), tc.req)
			tc.expect(t, err)
		})
	}
}

func TestDeleteImageNoAvailableSeedPeers(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	_, err = proxy.DeleteImage(context.Background(), NewDeleteImageRequest("example.com/foo/bar:1.0"))
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorContains(err, "failed to select seed peers")
}

func TestDeleteImageAllSeedPeers(t *testing.T) {
	tests := []struct {
		name   string
		failed string
		expect func(t *testing.T, resp *DeleteImageResponse, err error)
	}{
		{
			name: "every seed peer deletes the image",
			expect: func(t *testing.T, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.ElementsMatch([]*PeerDeletedImage{
					{IP: "127.0.0.1", Hostname: "seed-peer-1", DeletedTaskIDs: []string{"seed-peer-1-deleted"}, RetainedTaskIDs: []string{"seed-peer-1-retained"}},
					{IP: "127.0.0.1", Hostname: "seed-peer-2", DeletedTaskIDs: []string{"seed-peer-2-deleted"}, RetainedTaskIDs: []string{"seed-peer-2-retained"}},
					{IP: "127.0.0.1", Hostname: "seed-peer-3", DeletedTaskIDs: []string{"seed-peer-3-deleted"}, RetainedTaskIDs: []string{"seed-peer-3-retained"}},
				}, resp.Peers)
			},
		},
		{
			name:   "failed seed peer is left out",
			failed: "seed-peer-2",
			expect: func(t *testing.T, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.ElementsMatch([]*PeerDeletedImage{
					{IP: "127.0.0.1", Hostname: "seed-peer-1", DeletedTaskIDs: []string{"seed-peer-1-deleted"}, RetainedTaskIDs: []string{"seed-peer-1-retained"}},
					{IP: "127.0.0.1", Hostname: "seed-peer-3", DeletedTaskIDs: []string{"seed-peer-3-deleted"}, RetainedTaskIDs: []string{"seed-peer-3-retained"}},
				}, resp.Peers)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hosts []*commonv2.Host
			for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
				port := setupMockSeedPeerServer(t, &mockSeedPeer{
					deleteImage: func(*dfdaemonv2.DeleteImageRequest) (*dfdaemonv2.DeleteImageResponse, error) {
						if name == tc.failed {
							return nil, status.Error(codes.PermissionDenied, "delete failed")
						}

						return &dfdaemonv2.DeleteImageResponse{
							DeletedTaskIds:  []string{name + "-deleted"},
							RetainedTaskIds: []string{name + "-retained"},
						}, nil
					},
				})
				hosts = append(hosts, createSeedPeerHost(name, port, 0))
			}
			endpoint := setupMockScheduler(t, hosts)

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(t, err)
			defer proxy.Close()

			resp, err := proxy.DeleteImage(context.Background(), NewDeleteImageRequest("example.com/foo/bar:1.0"))
			tc.expect(t, resp, err)
		})
	}
}

func TestDeleteImageDeletesByManifestURL(t *testing.T) {
	tests := []struct {
		name   string
		req    *DeleteImageRequest
		expect func(t *testing.T, deleteImageRequest *dfdaemonv2.DeleteImageRequest)
	}{
		{
			name: "tag reference",
			req:  NewDeleteImageRequest("example.com/foo/bar:1.0"),
			expect: func(t *testing.T, deleteImageRequest *dfdaemonv2.DeleteImageRequest) {
				assert := assert.New(t)
				assert.Equal("https://example.com/v2/foo/bar/manifests/1.0", deleteImageRequest.GetUrl())
				assert.Nil(deleteImageRequest.Platform)
			},
		},
		{
			name: "digest reference",
			req:  NewDeleteImageRequest("example.com/foo/bar@sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d"),
			expect: func(t *testing.T, deleteImageRequest *dfdaemonv2.DeleteImageRequest) {
				assert := assert.New(t)
				assert.Equal("https://example.com/v2/foo/bar/manifests/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d", deleteImageRequest.GetUrl())
				assert.Nil(deleteImageRequest.Platform)
			},
		},
		{
			name: "docker hub reference",
			req:  NewDeleteImageRequest("nginx"),
			expect: func(t *testing.T, deleteImageRequest *dfdaemonv2.DeleteImageRequest) {
				assert := assert.New(t)
				assert.Equal("https://registry-1.docker.io/v2/library/nginx/manifests/latest", deleteImageRequest.GetUrl())
				assert.Nil(deleteImageRequest.Platform)
			},
		},
		{
			name: "platform",
			req:  NewDeleteImageRequest("example.com/foo/bar:1.0", WithDeleteImageRequestPlatform("linux/arm64")),
			expect: func(t *testing.T, deleteImageRequest *dfdaemonv2.DeleteImageRequest) {
				assert := assert.New(t)
				assert.Equal("https://example.com/v2/foo/bar/manifests/1.0", deleteImageRequest.GetUrl())
				assert.Equal("linux/arm64", deleteImageRequest.GetPlatform())
			},
		},
		{
			name: "platform is normalized",
			req:  NewDeleteImageRequest("example.com/foo/bar:1.0", WithDeleteImageRequestPlatform("linux/aarch64")),
			expect: func(t *testing.T, deleteImageRequest *dfdaemonv2.DeleteImageRequest) {
				assert := assert.New(t)
				assert.Equal("https://example.com/v2/foo/bar/manifests/1.0", deleteImageRequest.GetUrl())
				assert.Equal("linux/arm64", deleteImageRequest.GetPlatform())
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deleteImageRequests := make(chan *dfdaemonv2.DeleteImageRequest, 1)
			port := setupMockSeedPeerServer(t, &mockSeedPeer{
				deleteImage: func(req *dfdaemonv2.DeleteImageRequest) (*dfdaemonv2.DeleteImageResponse, error) {
					deleteImageRequests <- req
					return &dfdaemonv2.DeleteImageResponse{}, nil
				},
			})
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(t, err)
			defer proxy.Close()

			resp, err := proxy.DeleteImage(context.Background(), tc.req)
			assert.NoError(t, err)
			assert.Len(t, resp.Peers, 1)
			tc.expect(t, <-deleteImageRequests)
		})
	}
}

func TestDeleteImageRetriesOnlyTransientDeleteFailures(t *testing.T) {
	tests := []struct {
		name       string
		code       codes.Code
		maxRetries uint8
		expect     func(t *testing.T, hits int32, resp *DeleteImageResponse, err error)
	}{
		{
			name:       "internal",
			code:       codes.Internal,
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Empty(resp.Peers)
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "unavailable",
			code:       codes.Unavailable,
			maxRetries: 2,
			expect: func(t *testing.T, hits int32, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Empty(resp.Peers)
				assert.Equal(int32(3), hits)
			},
		},
		{
			name:       "resource exhausted",
			code:       codes.ResourceExhausted,
			maxRetries: 1,
			expect: func(t *testing.T, hits int32, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Empty(resp.Peers)
				assert.Equal(int32(2), hits)
			},
		},
		{
			name:       "not found",
			code:       codes.NotFound,
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Empty(resp.Peers)
				assert.Equal(int32(1), hits)
			},
		},
		{
			name:       "permission denied",
			code:       codes.PermissionDenied,
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Empty(resp.Peers)
				assert.Equal(int32(1), hits)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			port := setupMockSeedPeerServer(t, &mockSeedPeer{
				deleteImage: func(*dfdaemonv2.DeleteImageRequest) (*dfdaemonv2.DeleteImageResponse, error) {
					hits.Add(1)
					return nil, status.Error(tc.code, "delete failed")
				},
			})
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

			exponential := &backoff.ExponentialBackOff{InitialInterval: time.Millisecond, Multiplier: 2, MaxInterval: 2 * time.Millisecond}
			proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(tc.maxRetries), WithProxyBackoff(exponential))
			assert.NoError(t, err)
			defer proxy.Close()

			resp, err := proxy.DeleteImage(context.Background(), NewDeleteImageRequest("example.com/foo/bar:1.0"))
			tc.expect(t, hits.Load(), resp, err)
		})
	}
}

func TestDeleteImageFailsWhenTheContextEnds(t *testing.T) {
	tests := []struct {
		name   string
		cause  error
		expect func(t *testing.T, resp *DeleteImageResponse, err error)
	}{
		{
			name:  "cancelled",
			cause: context.Canceled,
			expect: func(t *testing.T, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, context.Canceled)
				assert.Empty(resp.Peers)
			},
		},
		{
			name:  "deadline exceeded",
			cause: context.DeadlineExceeded,
			expect: func(t *testing.T, resp *DeleteImageResponse, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, context.DeadlineExceeded)
				assert.Empty(resp.Peers)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)

			port := setupMockSeedPeerServer(t, &mockSeedPeer{
				deleteImage: func(*dfdaemonv2.DeleteImageRequest) (*dfdaemonv2.DeleteImageResponse, error) {
					cancel(tc.cause)
					return nil, status.Error(codes.Unavailable, "delete failed")
				},
			})
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(t, err)
			defer proxy.Close()

			resp, err := proxy.DeleteImage(ctx, NewDeleteImageRequest("example.com/foo/bar:1.0"))
			tc.expect(t, resp, err)
		})
	}
}

func TestDeleteNoAvailableSeedPeers(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	err = proxy.Delete(context.Background(), NewDeleteRequest("https://example.com/v2/foo/bar/blobs/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d"))
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorContains(err, "failed to select seed peers")
}

func TestPreheatAndDeleteHitSameSeedPeers(t *testing.T) {
	var mu sync.Mutex
	preheated := make(map[string]bool)
	deleted := make(map[string]bool)

	var hosts []*commonv2.Host
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		port := setupMockSeedPeerServer(t, &mockSeedPeer{
			onDownloadTask: func() {
				mu.Lock()
				preheated[name] = true
				mu.Unlock()
			},
			onDeleteTask: func() {
				mu.Lock()
				deleted[name] = true
				mu.Unlock()
			},
		})
		hosts = append(hosts, createSeedPeerHost(name, port, 0))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(t, err)
	defer proxy.Close()

	tests := []struct {
		url      string
		replicas int
	}{
		{"https://example.com/v2/foo/bar/blobs/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d", 1},
		{"https://example.com/v2/foo/bar/blobs/sha256:150b7321c0794448817b19fab51e415ff406ac8663c4f53d64c3590454dee201", 2},
		{"https://example.com/v2/foo/bar/manifests/sha256:e39d9c8ac4963d0b00a5af08678757b44c35ea8eb6be0cdfbeb1282e7f7e6003", 3},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d replicas", tt.replicas), func(t *testing.T) {
			assert := assert.New(t)

			mu.Lock()
			clear(preheated)
			clear(deleted)
			mu.Unlock()
			assert.NoError(proxy.Preheat(context.Background(), NewPreheatRequest(tt.url, WithPreheatRequestReplicas(tt.replicas))))

			mu.Lock()
			assert.Len(preheated, tt.replicas)
			mu.Unlock()

			assert.NoError(proxy.Delete(context.Background(), NewDeleteRequest(tt.url, WithDeleteRequestReplicas(tt.replicas))))

			mu.Lock()
			assert.Equal(preheated, deleted)
			mu.Unlock()
		})
	}
}

func TestPreheatAndDeleteAllSeedPeers(t *testing.T) {
	assert := assert.New(t)

	var mu sync.Mutex
	preheated := make(map[string]bool)
	deleted := make(map[string]bool)
	var hosts []*commonv2.Host
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		port := setupMockSeedPeerServer(t, &mockSeedPeer{
			onDownloadTask: func() {
				mu.Lock()
				preheated[name] = true
				mu.Unlock()
			},
			onDeleteTask: func() {
				mu.Lock()
				deleted[name] = true
				mu.Unlock()
			},
		})
		hosts = append(hosts, createSeedPeerHost(name, port, 0))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	url := "https://example.com/v2/foo/bar/blobs/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d"
	assert.NoError(proxy.Preheat(context.Background(), NewPreheatRequest(url, WithPreheatRequestReplicas(1), WithPreheatRequestScope(ScopeAllSeedPeers))))
	assert.NoError(proxy.Delete(context.Background(), NewDeleteRequest(url, WithDeleteRequestReplicas(1), WithDeleteRequestScope(ScopeAllSeedPeers))))

	mu.Lock()
	assert.Len(preheated, len(hosts))
	assert.Equal(preheated, deleted)
	mu.Unlock()
}

func TestDeleteRetriesOnlyTransientDeleteFailures(t *testing.T) {
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
				assert.ErrorIs(err, ErrInternal)
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
			name:       "not found counts as deleted",
			code:       codes.NotFound,
			maxRetries: 3,
			expect: func(t *testing.T, hits int32, err error) {
				assert := assert.New(t)
				assert.NoError(err)
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			port := setupMockSeedPeerServer(t, &mockSeedPeer{
				deleteTaskErr: status.Error(tc.code, "delete failed"),
				onDeleteTask:  func() { hits.Add(1) },
			})
			endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, 0)})

			exponential := &backoff.ExponentialBackOff{InitialInterval: time.Millisecond, Multiplier: 2, MaxInterval: 2 * time.Millisecond}
			proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(tc.maxRetries), WithProxyBackoff(exponential))
			assert.NoError(t, err)
			defer proxy.Close()

			err = proxy.Delete(context.Background(), NewDeleteRequest("http://example.com/payload.txt", WithDeleteRequestReplicas(1)))
			tc.expect(t, hits.Load(), err)
		})
	}
}
