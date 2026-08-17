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
	"net"
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	schedulerv2 "d7y.io/api/v2/pkg/apis/scheduler/v2"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// mockScheduler is a mock scheduler service which returns the given hosts as
// seed peers.
type mockScheduler struct {
	schedulerv2.UnimplementedSchedulerServer
	hosts []*commonv2.Host
}

func (s *mockScheduler) ListHosts(ctx context.Context, req *schedulerv2.ListHostsRequest) (*schedulerv2.ListHostsResponse, error) {
	return &schedulerv2.ListHostsResponse{Hosts: s.hosts}, nil
}

// setupMockScheduler starts a mock scheduler and returns its endpoint.
func setupMockScheduler(t *testing.T, hosts []*commonv2.Host) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	schedulerv2.RegisterSchedulerServer(server, &mockScheduler{hosts: hosts})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return fmt.Sprintf("http://%s", listener.Addr())
}

// mockSeedPeer is a mock seed peer service which responds to health checks and
// serves download task requests.
type mockSeedPeer struct {
	dfdaemonv2.UnimplementedDfdaemonUploadServer
	downloadErr error
}

func (s *mockSeedPeer) DownloadTask(req *dfdaemonv2.DownloadTaskRequest, stream dfdaemonv2.DfdaemonUpload_DownloadTaskServer) error {
	if s.downloadErr != nil {
		return s.downloadErr
	}

	for range 2 {
		if err := stream.Send(&dfdaemonv2.DownloadTaskResponse{
			HostId: "seed-peer-1",
			TaskId: "task-1",
			PeerId: "peer-1",
		}); err != nil {
			return err
		}
	}

	return nil
}

// setupMockSeedPeer starts a mock seed peer and returns its port.
func setupMockSeedPeer(t *testing.T, downloadErr error) int32 {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	dfdaemonv2.RegisterDfdaemonUploadServer(server, &mockSeedPeer{downloadErr: downloadErr})

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return int32(listener.Addr().(*net.TCPAddr).Port)
}

// createSeedPeerHost creates a seed peer host pointing at the mock seed peer
// server.
func createSeedPeerHost(name string, port int32) *commonv2.Host {
	return &commonv2.Host{
		Id:       name,
		Type:     1,
		Hostname: name,
		Ip:       "127.0.0.1",
		Port:     port,
		Name:     name,
	}
}

func TestNewSuccess(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	assert.Equal(uint8(1), proxy.maxRetries)
	proxy.Close()
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
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestTag("preheat"), WithPreheatRequestApplication("dfctl"))

	assert.NoError(proxy.Preheat(context.Background(), req))
}

func TestPreheatFailsWhenSeedPeerDownloadFails(t *testing.T) {
	assert := assert.New(t)
	port := setupMockSeedPeer(t, status.Error(codes.Internal, "storage is full"))
	endpoint := setupMockScheduler(t, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port)})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewPreheatRequest("http://example.com/payload.txt", WithPreheatRequestTag("preheat"), WithPreheatRequestApplication("dfctl"))

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

	// The platform must be in the format "os/arch" (e.g., "linux/amd64").
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

// TestLookupEndpoints asserts the fixed endpoint selection vectors
// shared with the Rust test suite (client-request/rust/src/lib.rs). Both sides
// must produce the same outputs; do not change one without the other.
func TestLookupEndpoints(t *testing.T) {
	assert := assert.New(t)

	// Start a mock seed peer for each name and record its endpoint.
	endpoints := make(map[string]string, 3)
	var hosts []*commonv2.Host
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		port := setupMockSeedPeer(t, nil)
		endpoints[name] = fmt.Sprintf("http://127.0.0.1:%d", port)
		hosts = append(hosts, createSeedPeerHost(name, port))
	}
	endpoint := setupMockScheduler(t, hosts)

	proxy, err := New(context.Background(), endpoint, WithProxyMaxRetries(3))
	assert.NoError(err)
	defer proxy.Close()

	tests := []struct {
		req   *GetRequest
		names []string
	}{
		{
			req: NewGetRequest(
				"https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar",
				WithGetRequestPieceLength(4194304),
				WithGetRequestTag("tag1"),
				WithGetRequestApplication("app1"),
				WithGetRequestFilteredQueryParams([]string{"Expires", "Signature"}),
			),
			names: []string{"seed-peer-1", "seed-peer-3", "seed-peer-1", "seed-peer-2"},
		},
		{
			req:   NewGetRequest("https://example.com/file.txt"),
			names: []string{"seed-peer-1", "seed-peer-2", "seed-peer-2", "seed-peer-1"},
		},
		{
			req: NewGetRequest(
				"https://example.com/file.txt",
				WithGetRequestContentForCalculatingTaskID("This is a test file"),
			),
			names: []string{"seed-peer-2", "seed-peer-3", "seed-peer-3", "seed-peer-1"},
		},
		{
			req:   NewGetRequest("http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"),
			names: []string{"seed-peer-3", "seed-peer-2", "seed-peer-1", "seed-peer-2"},
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

// TestTaskIDVectors asserts the fixed task id vectors shared with the Rust
// test suite (client-request/rust/tests/consistency.rs). Both sides must
// produce the same outputs; do not change one without the other.
func TestTaskIDVectors(t *testing.T) {
	assert := assert.New(t)

	pieceLength := uint64(4194304)
	id, err := generateTaskID(&GetRequest{
		url:                         "https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar",
		pieceLength:                 &pieceLength,
		tag:                         "tag1",
		application:                 "app1",
		filteredQueryParams:         []string{"Expires", "Signature"},
		enableTaskIDBasedBlobDigest: true,
	})
	assert.NoError(err)
	assert.Equal("2a0c4c713d7f2f65f36b78b79c4b78a6bf5d5f67b76730ed13485d3271482f1c", id)

	id, err = generateTaskID(&GetRequest{
		url:                         "https://example.com/file.txt",
		enableTaskIDBasedBlobDigest: true,
	})
	assert.NoError(err)
	assert.Equal("7fcf06e5f0b1e443065c1a563eed788eb2e168a05c6ad9c4b319f7a976322be0", id)

	id, err = generateTaskID(&GetRequest{
		contentForCalculatingTaskID: "This is a test file",
		enableTaskIDBasedBlobDigest: true,
	})
	assert.NoError(err)
	assert.Equal("e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249", id)

	id, err = generateTaskID(&GetRequest{
		url:                         "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
		enableTaskIDBasedBlobDigest: true,
	})
	assert.NoError(err)
	assert.Equal("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", id)
}

// TestTaskIDBlobDigestDisabled asserts that a blob url falls back to url based
// task id when the blob digest based task id is disabled.
func TestTaskIDBlobDigestDisabled(t *testing.T) {
	assert := assert.New(t)

	blobURL := "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
	id, err := generateTaskID(&GetRequest{url: blobURL})
	assert.NoError(err)
	assert.NotEqual("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", id)
}
