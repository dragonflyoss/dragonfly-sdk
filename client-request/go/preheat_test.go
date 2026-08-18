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
	"sync"
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
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

func TestPreheatImageInvalidReference(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatImageRequest("invalid image reference!!")
	err = proxy.PreheatImage(context.Background(), req)
	assert.ErrorContains(err, "invalid image reference")
}

func TestPreheatImageInvalidPlatform(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatImageRequest("docker.io/library/nginx:latest", WithPreheatImageRequestPlatform("linux-amd64"))
	err = proxy.PreheatImage(context.Background(), req)
	assert.Error(err)
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
		port := setupMockSeedPeerServer(t, &mockSeedPeer{onDownload: func() {
			mu.Lock()
			preheated[name] = true
			mu.Unlock()
		}})

		proxyPort := setupMockSeedPeerProxy(t, func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			served[name] = true
			mu.Unlock()
			fmt.Fprint(w, "ok")
		})

		hosts = append(hosts, createSeedPeerHost(name, port, proxyPort))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint)
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

			for range 10 {
				resp, err := proxy.Get(context.Background(), NewGetRequest(tt.url, WithGetRequestReplicas(tt.replicas)))
				assert.NoError(err)
				assert.NoError(resp.Body.Close())
			}

			mu.Lock()
			assert.Equal(preheated, served)
			mu.Unlock()
		})
	}
}
