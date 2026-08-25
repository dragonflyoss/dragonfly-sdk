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
	"testing"

	schedulerv2 "d7y.io/api/v2/pkg/apis/scheduler/v2"
	managertypes "d7y.io/dragonfly/v2/manager/types"
	"d7y.io/dragonfly/v2/pkg/idgen"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestStatImageQueriesSeedPeers(t *testing.T) {
	assert := assert.New(t)

	statImageRequests := make(chan *schedulerv2.StatImageRequest, 1)
	endpoint := setupMockSchedulerServer(t, &mockScheduler{
		statImage: func(req *schedulerv2.StatImageRequest) (*schedulerv2.StatImageResponse, error) {
			statImageRequests <- req
			return &schedulerv2.StatImageResponse{
				Image: &schedulerv2.Image{Layers: []*schedulerv2.Layer{
					{Url: "https://example.com/v2/foo/bar/blobs/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d"},
					{Url: "https://example.com/v2/foo/bar/blobs/sha256:150b7321c0794448817b19fab51e415ff406ac8663c4f53d64c3590454dee201"},
				}},
				Peers: []*schedulerv2.PeerImage{
					{
						Ip:       "127.0.0.1",
						Hostname: "seed-peer-1",
						CachedLayers: []*schedulerv2.Layer{
							{Url: "https://example.com/v2/foo/bar/blobs/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d", IsFinished: proto.Bool(true)},
							{Url: "https://example.com/v2/foo/bar/blobs/sha256:150b7321c0794448817b19fab51e415ff406ac8663c4f53d64c3590454dee201"},
						},
					},
				},
			}, nil
		},
	})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewStatImageRequest("example.com/foo/bar:1.0",
		WithStatImageRequestAuth("user", "pass"),
		WithStatImageRequestPlatform("linux/amd64"),
		WithStatImageRequestPieceLength(4194304),
		WithStatImageRequestTag("stat"),
		WithStatImageRequestApplication("dfctl"),
	)

	resp, err := proxy.StatImage(context.Background(), req)
	assert.NoError(err)

	statImageRequest := <-statImageRequests
	assert.Equal("https://example.com/v2/foo/bar/manifests/1.0", statImageRequest.GetUrl())
	assert.Equal(managertypes.AllSeedPeersScope, statImageRequest.GetScope())
	assert.Equal("user", statImageRequest.GetUsername())
	assert.Equal("pass", statImageRequest.GetPassword())
	assert.Equal("linux/amd64", statImageRequest.GetPlatform())
	assert.Equal(uint64(4194304), statImageRequest.GetPieceLength())
	assert.Equal("stat", statImageRequest.GetTag())
	assert.Equal("dfctl", statImageRequest.GetApplication())
	assert.Equal(idgen.DefaultFilteredQueryParams, statImageRequest.GetFilteredQueryParams())
	assert.Equal(defaultRequestTimeout, statImageRequest.GetTimeout().AsDuration())

	assert.Len(resp.Layers, 2)
	assert.Len(resp.Peers, 1)
	assert.Equal("127.0.0.1", resp.Peers[0].IP)
	assert.Equal("seed-peer-1", resp.Peers[0].Hostname)
	assert.Equal([]*Layer{
		{URL: "https://example.com/v2/foo/bar/blobs/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d", IsFinished: true},
		{URL: "https://example.com/v2/foo/bar/blobs/sha256:150b7321c0794448817b19fab51e415ff406ac8663c4f53d64c3590454dee201", IsFinished: false},
	}, resp.Peers[0].CachedLayers)
}

func TestStatImageOmitsEmptyOptionalFields(t *testing.T) {
	assert := assert.New(t)

	statImageRequests := make(chan *schedulerv2.StatImageRequest, 1)
	endpoint := setupMockSchedulerServer(t, &mockScheduler{
		statImage: func(req *schedulerv2.StatImageRequest) (*schedulerv2.StatImageResponse, error) {
			statImageRequests <- req
			return &schedulerv2.StatImageResponse{}, nil
		},
	})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewStatImageRequest("example.com/foo/bar:1.0")

	resp, err := proxy.StatImage(context.Background(), req)
	assert.NoError(err)
	assert.Empty(resp.Layers)
	assert.Empty(resp.Peers)

	statImageRequest := <-statImageRequests
	assert.Nil(statImageRequest.Tag)
	assert.Nil(statImageRequest.Application)
	assert.Nil(statImageRequest.Username)
	assert.Nil(statImageRequest.Password)
	assert.Nil(statImageRequest.Platform)
	assert.Nil(statImageRequest.PieceLength)
}

func TestStatImageWithDigestReference(t *testing.T) {
	assert := assert.New(t)

	statImageRequests := make(chan *schedulerv2.StatImageRequest, 1)
	endpoint := setupMockSchedulerServer(t, &mockScheduler{
		statImage: func(req *schedulerv2.StatImageRequest) (*schedulerv2.StatImageResponse, error) {
			statImageRequests <- req
			return &schedulerv2.StatImageResponse{}, nil
		},
	})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewStatImageRequest("example.com/foo/bar@sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d")

	_, err = proxy.StatImage(context.Background(), req)
	assert.NoError(err)

	statImageRequest := <-statImageRequests
	assert.Equal("https://example.com/v2/foo/bar/manifests/sha256:b5f4dfca35398b36f61baa60e2bf2c242401c9d7db3de9168dcf780a2feedd2d", statImageRequest.GetUrl())
}

func TestStatImageNormalizesDockerHubReference(t *testing.T) {
	assert := assert.New(t)

	statImageRequests := make(chan *schedulerv2.StatImageRequest, 1)
	endpoint := setupMockSchedulerServer(t, &mockScheduler{
		statImage: func(req *schedulerv2.StatImageRequest) (*schedulerv2.StatImageResponse, error) {
			statImageRequests <- req
			return &schedulerv2.StatImageResponse{}, nil
		},
	})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewStatImageRequest("nginx")

	_, err = proxy.StatImage(context.Background(), req)
	assert.NoError(err)

	statImageRequest := <-statImageRequests
	assert.Equal("https://registry-1.docker.io/v2/library/nginx/manifests/latest", statImageRequest.GetUrl())
}

func TestStatImageInvalidImageReference(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockScheduler(t, nil)

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewStatImageRequest("INVALID IMAGE")

	_, err = proxy.StatImage(context.Background(), req)
	assert.ErrorIs(err, ErrInvalidArgument)
}

func TestStatImageSchedulerError(t *testing.T) {
	assert := assert.New(t)
	endpoint := setupMockSchedulerServer(t, &mockScheduler{
		statImage: func(req *schedulerv2.StatImageRequest) (*schedulerv2.StatImageResponse, error) {
			return nil, status.Error(codes.Internal, "storage is full")
		},
	})

	proxy, err := New(context.Background(), endpoint)
	assert.NoError(err)
	defer proxy.Close()

	req := NewStatImageRequest("example.com/foo/bar:1.0")

	_, err = proxy.StatImage(context.Background(), req)
	assert.ErrorIs(err, ErrInternal)
	assert.ErrorContains(err, "failed to stat image")
}
