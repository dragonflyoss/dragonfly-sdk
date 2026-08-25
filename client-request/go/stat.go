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
	"math"

	schedulerv2 "d7y.io/api/v2/pkg/apis/scheduler/v2"
	managertypes "d7y.io/dragonfly/v2/manager/types"
	"d7y.io/dragonfly/v2/pkg/oci"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
)

// StatImage provides detailed status for an OCI image's distribution in the
// Dragonfly. It parses the image reference and requests the scheduler to
// resolve the image manifest and collect the cached layers on each peer. It
// only queries the seed peers.
func (p *Proxy) StatImage(ctx context.Context, req *StatImageRequest) (*StatImageResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, req.timeout)
	defer cancel()

	ref, err := oci.ParseImage(req.image)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	statImageRequest := &schedulerv2.StatImageRequest{
		Url:                 ref.ManifestURL(),
		PieceLength:         req.pieceLength,
		FilteredQueryParams: req.filteredQueryParams,
		Timeout:             durationpb.New(req.timeout),
		Scope:               managertypes.AllSeedPeersScope,
	}

	if req.tag != "" {
		statImageRequest.Tag = &req.tag
	}

	if req.application != "" {
		statImageRequest.Application = &req.application
	}

	if req.username != "" {
		statImageRequest.Username = &req.username
	}

	if req.password != "" {
		statImageRequest.Password = &req.password
	}

	if req.platform != "" {
		statImageRequest.Platform = &req.platform
	}

	resp, err := schedulerv2.NewSchedulerClient(p.schedulerConn).StatImage(ctx, statImageRequest,
		grpc.MaxCallRecvMsgSize(math.MaxInt32),
		grpc.MaxCallSendMsgSize(math.MaxInt32),
	)
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			return nil, fmt.Errorf("%w: failed to stat image %s: %v", ErrInvalidArgument, req.image, err)
		}

		return nil, fmt.Errorf("%w: failed to stat image %s: %v", ErrInternal, req.image, err)
	}

	layers := make([]string, 0, len(resp.GetImage().GetLayers()))
	for _, layer := range resp.GetImage().GetLayers() {
		layers = append(layers, layer.GetUrl())
	}

	peers := make([]*PeerImage, 0, len(resp.GetPeers()))
	for _, peer := range resp.GetPeers() {
		cachedLayers := make([]*Layer, 0, len(peer.GetCachedLayers()))
		for _, layer := range peer.GetCachedLayers() {
			cachedLayers = append(cachedLayers, &Layer{
				URL:        layer.GetUrl(),
				IsFinished: layer.GetIsFinished(),
			})
		}

		peers = append(peers, &PeerImage{
			IP:           peer.GetIp(),
			Hostname:     peer.GetHostname(),
			CachedLayers: cachedLayers,
		})
	}

	return &StatImageResponse{Layers: layers, Peers: peers}, nil
}
