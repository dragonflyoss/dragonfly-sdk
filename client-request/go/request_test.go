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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	schedulerv2 "d7y.io/api/v2/pkg/apis/scheduler/v2"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type mockScheduler struct {
	schedulerv2.UnimplementedSchedulerServer
	hosts     []*commonv2.Host
	statImage func(*schedulerv2.StatImageRequest) (*schedulerv2.StatImageResponse, error)
}

func (s *mockScheduler) ListHosts(ctx context.Context, req *schedulerv2.ListHostsRequest) (*schedulerv2.ListHostsResponse, error) {
	return &schedulerv2.ListHostsResponse{Hosts: s.hosts}, nil
}

func (s *mockScheduler) StatImage(ctx context.Context, req *schedulerv2.StatImageRequest) (*schedulerv2.StatImageResponse, error) {
	if s.statImage != nil {
		return s.statImage(req)
	}

	return s.UnimplementedSchedulerServer.StatImage(ctx, req)
}

func setupMockScheduler(t testing.TB, hosts []*commonv2.Host) string {
	return setupMockSchedulerServer(t, &mockScheduler{hosts: hosts})
}

func setupMockSchedulerServer(t testing.TB, scheduler *mockScheduler) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	schedulerv2.RegisterSchedulerServer(server, scheduler)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return fmt.Sprintf("http://%s", listener.Addr())
}

type mockSeedPeer struct {
	dfdaemonv2.UnimplementedDfdaemonUploadServer
	downloadErr error
	onDownload  func()
}

func (s *mockSeedPeer) DownloadTask(req *dfdaemonv2.DownloadTaskRequest, stream dfdaemonv2.DfdaemonUpload_DownloadTaskServer) error {
	if s.onDownload != nil {
		s.onDownload()
	}

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

func setupMockSeedPeer(t testing.TB, downloadErr error) int32 {
	return setupMockSeedPeerServer(t, &mockSeedPeer{downloadErr: downloadErr})
}

func setupMockSeedPeerServer(t testing.TB, peer *mockSeedPeer) int32 {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	server := grpc.NewServer()
	dfdaemonv2.RegisterDfdaemonUploadServer(server, peer)

	healthServer := health.NewServer()
	healthServer.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(server, healthServer)

	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	return int32(listener.Addr().(*net.TCPAddr).Port)
}

func createSeedPeerHost(name string, port, proxyPort int32) *commonv2.Host {
	return &commonv2.Host{
		Id:        name,
		Type:      1,
		Hostname:  name,
		Ip:        "127.0.0.1",
		Port:      port,
		ProxyPort: proxyPort,
		Name:      name,
	}
}

func setupMockSeedPeerProxy(t testing.TB, handler http.HandlerFunc) int32 {
	t.Helper()

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	port, err := strconv.Atoi(server.URL[strings.LastIndex(server.URL, ":")+1:])
	if err != nil {
		t.Fatal(err)
	}

	return int32(port)
}

func TestNewRequestDefaults(t *testing.T) {
	assert := assert.New(t)

	get := NewGetRequest("https://example.com/file.txt")
	assert.NotEmpty(get.filteredQueryParams)
	assert.True(get.enableTaskIDBasedBlobDigest)
	assert.Equal(2, get.replicas)
	assert.Equal(30*time.Minute, get.timeout)

	preheat := NewPreheatRequest("https://example.com/file.txt")
	assert.NotEmpty(preheat.filteredQueryParams)
	assert.True(preheat.enableTaskIDBasedBlobDigest)
	assert.Equal(2, preheat.replicas)
	assert.Equal(30*time.Minute, preheat.timeout)

	image := NewPreheatImageRequest("docker.io/library/nginx:latest")
	assert.Equal(2, image.replicas)
	assert.Equal(4, image.concurrentTaskCount)
	assert.Equal(30*time.Minute, image.timeout)

	stat := NewStatImageRequest("docker.io/library/nginx:latest")
	assert.NotEmpty(stat.filteredQueryParams)
	assert.True(stat.enableTaskIDBasedBlobDigest)
	assert.Equal(30*time.Minute, stat.timeout)
}

func setupBenchProxy(b *testing.B, body []byte) (*Proxy, string) {
	b.Helper()

	proxyPort := setupMockSeedPeerProxy(b, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	})
	port := setupMockSeedPeer(b, nil)
	endpoint := setupMockScheduler(b, []*commonv2.Host{createSeedPeerHost("seed-peer-1", port, proxyPort)})

	proxy, err := New(context.Background(), endpoint)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { proxy.Close() })

	return proxy, fmt.Sprintf("http://127.0.0.1:%d", proxyPort)
}

func BenchmarkGet(b *testing.B) {
	proxy, _ := setupBenchProxy(b, make([]byte, 64*1024))
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1))

	b.ReportAllocs()
	for b.Loop() {
		resp, err := proxy.Get(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}

		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkGetInto(b *testing.B) {
	proxy, _ := setupBenchProxy(b, make([]byte, 64*1024))
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1))

	b.ReportAllocs()
	for b.Loop() {
		if _, err := proxy.GetInto(context.Background(), req, io.Discard); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetWithEndpoints(b *testing.B) {
	_, endpoint := setupBenchProxy(b, make([]byte, 64*1024))
	proxy, err := NewWithEndpoints([]string{endpoint})
	if err != nil {
		b.Fatal(err)
	}
	req := NewGetRequest("http://example.com/file.txt", WithGetRequestReplicas(1))

	b.ReportAllocs()
	for b.Loop() {
		resp, err := proxy.Get(context.Background(), req)
		if err != nil {
			b.Fatal(err)
		}

		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			b.Fatal(err)
		}
		resp.Body.Close()
	}
}

func BenchmarkLookupEndpoints(b *testing.B) {
	proxy, _ := setupBenchProxy(b, nil)
	req := NewGetRequest("http://example.com/file.txt")

	b.ReportAllocs()
	for b.Loop() {
		if _, err := proxy.LookupEndpoints(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkPreheat(b *testing.B) {
	proxy, _ := setupBenchProxy(b, nil)
	req := NewPreheatRequest("http://example.com/file.txt", WithPreheatRequestReplicas(1))

	b.ReportAllocs()
	for b.Loop() {
		if err := proxy.Preheat(context.Background(), req); err != nil {
			b.Fatal(err)
		}
	}
}
