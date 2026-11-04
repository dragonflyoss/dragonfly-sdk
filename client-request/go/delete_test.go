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
	"github.com/cenkalti/backoff/v5"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDeleteImageInvalidReference(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	err = proxy.DeleteImage(context.Background(), NewDeleteImageRequest("invalid image reference!!"))
	assert.ErrorIs(err, ErrInvalidArgument)
	assert.ErrorContains(err, "invalid image reference")
}

func TestDeleteImageInvalidArguments(t *testing.T) {
	tests := []struct {
		name string
		req  *DeleteImageRequest
	}{
		{
			name: "zero replicas",
			req:  NewDeleteImageRequest("docker.io/library/nginx:latest", WithDeleteImageRequestReplicas(0)),
		},
		{
			name: "zero concurrent task count",
			req:  NewDeleteImageRequest("docker.io/library/nginx:latest", WithDeleteImageRequestConcurrentTaskCount(0)),
		},
		{
			name: "invalid scope",
			req:  NewDeleteImageRequest("docker.io/library/nginx:latest", WithDeleteImageRequestScope(Scope("all_peers"))),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert := assert.New(t)
			endpoint := setupMockScheduler(t, nil)

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(err)
			defer proxy.Close()

			assert.ErrorIs(proxy.DeleteImage(context.Background(), tc.req), ErrInvalidArgument)
		})
	}
}

func TestDeleteImageInvalidPlatform(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewDeleteImageRequest("docker.io/library/nginx:latest", WithDeleteImageRequestPlatform("linux-amd64"))
	err = proxy.DeleteImage(context.Background(), req)
	assert.ErrorContains(err, "invalid platform format")
}

func TestDeleteImageUnreachableRegistry(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewDeleteImageRequest("127.0.0.1:1/library/nginx:latest", WithDeleteImageRequestTimeout(5*time.Second))
	err = proxy.DeleteImage(context.Background(), req)
	assert.ErrorIs(err, ErrInternal)
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
			onDownload: func() {
				mu.Lock()
				preheated[name] = true
				mu.Unlock()
			},
			onDelete: func() {
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
			onDownload: func() {
				mu.Lock()
				preheated[name] = true
				mu.Unlock()
			},
			onDelete: func() {
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
				deleteErr: status.Error(tc.code, "delete failed"),
				onDelete:  func() { hits.Add(1) },
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
