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
	"net"
	"slices"
	"strconv"

	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	"d7y.io/dragonfly/v2/pkg/idgen"
	"d7y.io/dragonfly/v2/pkg/net/ip"
	"d7y.io/dragonfly/v2/pkg/oci"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// StatImage provides detailed status for an OCI image's distribution in the
// Dragonfly. It resolves the image manifests, all the platforms or the
// specified one, and asks every healthy seed peer for the layers it has cached
// by the dfdaemon stat local task API. A seed peer that fails is left out of
// the response, an ended context fails the request.
func (p *Proxy) StatImage(ctx context.Context, req *StatImageRequest) (*StatImageResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	ref, err := oci.ParseImage(req.image)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	opts := []oci.ResolveOption{
		oci.WithAuth(req.username, req.password),
		oci.WithHTTPClient(p.registryClient),
	}

	if req.platform != nil {
		opts = append(opts, oci.WithPlatform(*req.platform))
	}

	manifests, _, err := oci.Resolve(ctx, ref, opts...)
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return nil, cause
		}

		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}

	// Collect the layers of the matched platforms. A layer shared by several
	// platforms is stated once.
	var layers []string
	for _, manifest := range manifests {
		for _, blobURL := range manifest.BlobURLs {
			if !slices.Contains(layers, blobURL) {
				layers = append(layers, blobURL)
			}
		}
	}

	// Construct the stat local task requests of the layers.
	remoteIP := ip.IPv4.String()
	statLocalTaskRequests := make([]*dfdaemonv2.StatLocalTaskRequest, 0, len(layers))
	for _, layer := range layers {
		id, err := idgen.TaskIDV2(layer, req.pieceLength, req.tag, req.application, req.filteredQueryParams, "", req.enableTaskIDBasedBlobDigest)
		if err != nil {
			return nil, fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
		}

		statLocalTaskRequests = append(statLocalTaskRequests, &dfdaemonv2.StatLocalTaskRequest{TaskId: id, RemoteIp: &remoteIP})
	}

	// Select all seed peers.
	seedPeers, err := p.seedPeerSelector.SelectAll()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to select seed peers: %v", ErrInternal, err)
	}

	// Stat the layers on every seed peer concurrently.
	peers := make([]*PeerStatedImage, len(seedPeers))
	var g errgroup.Group
	g.SetLimit(req.concurrentPeerCount)
	for i, peer := range seedPeers {
		g.Go(func() error {
			addr := net.JoinHostPort(peer.Ip, strconv.Itoa(int(peer.Port)))
			return p.retry.run(ctx, func() error {
				conn, err := grpc.NewClient(
					addr,
					grpc.WithTransportCredentials(insecure.NewCredentials()),
					grpc.WithDefaultCallOptions(
						grpc.MaxCallRecvMsgSize(math.MaxInt32),
						grpc.MaxCallSendMsgSize(math.MaxInt32),
					),
				)
				if err != nil {
					return fmt.Errorf("%w: failed to connect to seed peer %s: %v", ErrInternal, addr, err)
				}
				defer conn.Close()

				// A NotFound answer means the seed peer has not cached the layer.
				var cachedLayers []*Layer
				client := dfdaemonv2.NewDfdaemonUploadClient(conn)
				for j, statLocalTaskRequest := range statLocalTaskRequests {
					ctx, cancel := context.WithTimeout(ctx, req.timeout)
					resp, err := client.StatLocalTask(ctx, statLocalTaskRequest)
					cancel()
					if status.Code(err) == codes.NotFound {
						continue
					}

					if err != nil {
						return fmt.Errorf("%w: failed to stat task %s: %w", ErrInternal, statLocalTaskRequest.GetTaskId(), err)
					}

					cachedLayers = append(cachedLayers, &Layer{URL: layers[j], IsFinished: resp.GetFinishedAt() != nil})
				}

				if len(cachedLayers) > 0 {
					peers[i] = &PeerStatedImage{IP: peer.Ip, Hostname: peer.Hostname, CachedLayers: cachedLayers}
				}

				return nil
			})
		})
	}

	// A seed peer that fails is left out of the response.
	_ = g.Wait()
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}

	return &StatImageResponse{Layers: layers, Peers: slices.DeleteFunc(peers, func(peer *PeerStatedImage) bool { return peer == nil })}, nil
}
