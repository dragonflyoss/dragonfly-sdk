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
	"testing"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	"d7y.io/dragonfly/v2/pkg/idgen"
	"d7y.io/dragonfly/v2/pkg/net/ip"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStatImageInvalidArgument(t *testing.T) {
	tests := []struct {
		name   string
		req    *StatImageRequest
		expect func(t *testing.T, err error)
	}{
		{
			name: "invalid reference",
			req:  NewStatImageRequest("INVALID IMAGE"),
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.ErrorContains(err, "invalid image reference")
			},
		},
		{
			name: "invalid platform",
			req:  NewStatImageRequest("docker.io/library/nginx:latest", WithStatImageRequestPlatform("linux-amd64")),
			expect: func(t *testing.T, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, ErrInvalidArgument)
				assert.ErrorContains(err, "invalid platform format")
			},
		},
		{
			name: "invalid concurrent peer count",
			req:  NewStatImageRequest("docker.io/library/nginx:latest", WithStatImageRequestConcurrentPeerCount(0)),
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

			_, err = proxy.StatImage(context.Background(), tc.req)
			tc.expect(t, err)
		})
	}
}

func TestStatImageUnreachableRegistry(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewStatImageRequest("127.0.0.1:1/library/nginx:latest", WithStatImageRequestTimeout(5*time.Second))

	_, err = proxy.StatImage(context.Background(), req)
	assert.ErrorIs(err, ErrInternal)
}

func TestStatImageStatsLayersOnSeedPeers(t *testing.T) {
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

	amd64ConfigURL := fmt.Sprintf("https://%s/v2/foo/bar/blobs/sha256:b5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7", registry)
	arm64ConfigURL := fmt.Sprintf("https://%s/v2/foo/bar/blobs/sha256:c5b2b2c507a0944348e0303114d8d93aaaa081732b86451d9bce1f432a537bc7", registry)
	layerURL := fmt.Sprintf("https://%s/v2/foo/bar/blobs/sha256:e692418e4cbaf90ca69d05a66403747baa33ee08806650b51fab815ad7fc331f", registry)
	layerTaskID, err := idgen.TaskIDV2(layerURL, nil, "", "", idgen.DefaultFilteredQueryParams, "", true)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name          string
		req           *StatImageRequest
		statLocalTask func(name string, req *dfdaemonv2.StatLocalTaskRequest, cancel context.CancelCauseFunc) (*dfdaemonv2.StatLocalTaskResponse, error)
		expect        func(t *testing.T, resp *StatImageResponse, err error)
	}{
		{
			name: "all platforms",
			req:  NewStatImageRequest(registry + "/foo/bar:1.0"),
			statLocalTask: func(name string, req *dfdaemonv2.StatLocalTaskRequest, cancel context.CancelCauseFunc) (*dfdaemonv2.StatLocalTaskResponse, error) {
				if name == "seed-peer-2" {
					return nil, status.Error(codes.NotFound, "local task not found")
				}

				return &dfdaemonv2.StatLocalTaskResponse{TaskId: req.GetTaskId(), FinishedAt: timestamppb.Now()}, nil
			},
			expect: func(t *testing.T, resp *StatImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal([]string{amd64ConfigURL, layerURL, arm64ConfigURL}, resp.Layers)
				assert.Equal([]*PeerStatedImage{{
					IP:       "127.0.0.1",
					Hostname: "seed-peer-1",
					CachedLayers: []*Layer{
						{URL: amd64ConfigURL, IsFinished: true},
						{URL: layerURL, IsFinished: true},
						{URL: arm64ConfigURL, IsFinished: true},
					},
				}}, resp.Peers)
			},
		},
		{
			name: "platform",
			req:  NewStatImageRequest(registry+"/foo/bar:1.0", WithStatImageRequestPlatform("linux/arm64")),
			statLocalTask: func(name string, req *dfdaemonv2.StatLocalTaskRequest, cancel context.CancelCauseFunc) (*dfdaemonv2.StatLocalTaskResponse, error) {
				if name == "seed-peer-2" {
					return nil, status.Error(codes.NotFound, "local task not found")
				}

				return &dfdaemonv2.StatLocalTaskResponse{TaskId: req.GetTaskId(), FinishedAt: timestamppb.Now()}, nil
			},
			expect: func(t *testing.T, resp *StatImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal([]string{arm64ConfigURL, layerURL}, resp.Layers)
				assert.Equal([]*PeerStatedImage{{
					IP:       "127.0.0.1",
					Hostname: "seed-peer-1",
					CachedLayers: []*Layer{
						{URL: arm64ConfigURL, IsFinished: true},
						{URL: layerURL, IsFinished: true},
					},
				}}, resp.Peers)
			},
		},
		{
			name: "unfinished layer cached by one seed peer",
			req:  NewStatImageRequest(registry + "/foo/bar:1.0"),
			statLocalTask: func(name string, req *dfdaemonv2.StatLocalTaskRequest, cancel context.CancelCauseFunc) (*dfdaemonv2.StatLocalTaskResponse, error) {
				if name == "seed-peer-1" && req.GetTaskId() == layerTaskID {
					return &dfdaemonv2.StatLocalTaskResponse{TaskId: req.GetTaskId()}, nil
				}

				return nil, status.Error(codes.NotFound, "local task not found")
			},
			expect: func(t *testing.T, resp *StatImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal([]string{amd64ConfigURL, layerURL, arm64ConfigURL}, resp.Layers)
				assert.Equal([]*PeerStatedImage{{
					IP:           "127.0.0.1",
					Hostname:     "seed-peer-1",
					CachedLayers: []*Layer{{URL: layerURL, IsFinished: false}},
				}}, resp.Peers)
			},
		},
		{
			name: "failed seed peer is left out",
			req:  NewStatImageRequest(registry + "/foo/bar:1.0"),
			statLocalTask: func(name string, req *dfdaemonv2.StatLocalTaskRequest, cancel context.CancelCauseFunc) (*dfdaemonv2.StatLocalTaskResponse, error) {
				if name == "seed-peer-1" {
					return nil, status.Error(codes.PermissionDenied, "stat failed")
				}

				return &dfdaemonv2.StatLocalTaskResponse{TaskId: req.GetTaskId(), FinishedAt: timestamppb.Now()}, nil
			},
			expect: func(t *testing.T, resp *StatImageResponse, err error) {
				assert := assert.New(t)
				assert.NoError(err)
				assert.Equal([]string{amd64ConfigURL, layerURL, arm64ConfigURL}, resp.Layers)
				assert.Equal([]*PeerStatedImage{{
					IP:       "127.0.0.1",
					Hostname: "seed-peer-2",
					CachedLayers: []*Layer{
						{URL: amd64ConfigURL, IsFinished: true},
						{URL: layerURL, IsFinished: true},
						{URL: arm64ConfigURL, IsFinished: true},
					},
				}}, resp.Peers)
			},
		},
		{
			name: "cancelled context",
			req:  NewStatImageRequest(registry + "/foo/bar:1.0"),
			statLocalTask: func(name string, req *dfdaemonv2.StatLocalTaskRequest, cancel context.CancelCauseFunc) (*dfdaemonv2.StatLocalTaskResponse, error) {
				cancel(context.Canceled)
				return nil, status.Error(codes.Unavailable, "stat failed")
			},
			expect: func(t *testing.T, resp *StatImageResponse, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, context.Canceled)
				assert.Nil(resp)
			},
		},
		{
			name: "deadline exceeded context",
			req:  NewStatImageRequest(registry + "/foo/bar:1.0"),
			statLocalTask: func(name string, req *dfdaemonv2.StatLocalTaskRequest, cancel context.CancelCauseFunc) (*dfdaemonv2.StatLocalTaskResponse, error) {
				cancel(context.DeadlineExceeded)
				return nil, status.Error(codes.Unavailable, "stat failed")
			},
			expect: func(t *testing.T, resp *StatImageResponse, err error) {
				assert := assert.New(t)
				assert.ErrorIs(err, context.DeadlineExceeded)
				assert.Nil(resp)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)

			var hosts []*commonv2.Host
			for _, name := range []string{"seed-peer-1", "seed-peer-2"} {
				port := setupMockSeedPeerServer(t, &mockSeedPeer{
					statLocalTask: func(req *dfdaemonv2.StatLocalTaskRequest) (*dfdaemonv2.StatLocalTaskResponse, error) {
						return tc.statLocalTask(name, req, cancel)
					},
				})
				hosts = append(hosts, createSeedPeerHost(name, port, 0))
			}
			endpoint := setupMockScheduler(t, hosts)

			proxy, err := New(context.Background(), endpoint)
			assert.NoError(t, err)
			defer proxy.Close()

			resp, err := proxy.StatImage(ctx, tc.req)
			tc.expect(t, resp, err)
		})
	}
}
