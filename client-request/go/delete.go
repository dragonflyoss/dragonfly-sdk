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
	"strconv"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
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

// Delete deletes a preheated file from the replicas of seed peers, or from all
// seed peers with ScopeAllSeedPeers, via the Dragonfly. It has every selected
// seed peer delete the task by the dfdaemon delete task API, clamping the
// replicas to the available seed peers instead of failing when fewer are
// available.
func (p *Proxy) Delete(ctx context.Context, req *DeleteRequest) error {
	if err := req.validate(); err != nil {
		return err
	}

	// Generate task id for selecting seed peer.
	id, err := idgen.TaskIDV2(req.url, req.pieceLength, req.tag, req.application, req.filteredQueryParams, req.contentForCalculatingTaskID, req.enableTaskIDBasedBlobDigest)
	if err != nil {
		return fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
	}

	// Select seed peers serving the task.
	seedPeers, err := p.selectSeedPeers(id, req.scope, req.replicas)
	if err != nil {
		return fmt.Errorf("%w: failed to select seed peers: %v", ErrInternal, err)
	}

	// Construct the delete task request.
	remoteIP := ip.IPv4.String()
	deleteTaskRequest := &dfdaemonv2.DeleteTaskRequest{TaskId: id, RemoteIp: &remoteIP}

	// Delete the task from every replica seed peer concurrently and wait for
	// the deletes to finish. A transient failure is retried on the same seed
	// peer, so the task leaves every replica.
	g, ctx := errgroup.WithContext(ctx)
	for _, peer := range seedPeers {
		g.Go(func() error {
			return p.deleteTask(ctx, peer, deleteTaskRequest, req.timeout)
		})
	}

	return g.Wait()
}

// DeleteImage deletes a preheated OCI image from every healthy seed peer by the
// dfdaemon delete image API, for all its platforms or the specified one. A task
// still referenced by another image or platform is retained. It fails if any
// seed peer fails.
func (p *Proxy) DeleteImage(ctx context.Context, req *DeleteImageRequest) (*DeleteImageResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	ref, err := oci.ParseImage(req.image)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	// Select all seed peers.
	seedPeers, err := p.seedPeerSelector.SelectAll()
	if err != nil {
		return nil, fmt.Errorf("%w: failed to select seed peers: %v", ErrInternal, err)
	}

	// Construct the delete image request.
	remoteIP := ip.IPv4.String()
	deleteImageRequest := &dfdaemonv2.DeleteImageRequest{Url: ref.ManifestURL(), Platform: req.platform, RemoteIp: &remoteIP}

	// Delete the image from every seed peer concurrently.
	peers := make([]*PeerDeletedImage, len(seedPeers))
	g, ctx := errgroup.WithContext(ctx)
	for i, peer := range seedPeers {
		g.Go(func() error {
			resp, err := p.deleteImage(ctx, peer, deleteImageRequest, req.timeout)
			if err != nil {
				return err
			}

			peers[i] = &PeerDeletedImage{
				IP:              peer.Ip,
				Hostname:        peer.Hostname,
				DeletedTaskIDs:  resp.GetDeletedTaskIds(),
				RetainedTaskIDs: resp.GetRetainedTaskIds(),
			}

			return nil
		})
	}

	if err := g.Wait(); err != nil {
		return nil, err
	}

	return &DeleteImageResponse{Peers: peers}, nil
}

// deleteTask has the seed peer delete the task, retrying a transient failure on
// the same seed peer. Each attempt runs under the timeout. A NotFound answer
// counts as deleted, aligned with the delete task job of the scheduler.
func (p *Proxy) deleteTask(ctx context.Context, peer *commonv2.Host, deleteTaskRequest *dfdaemonv2.DeleteTaskRequest, timeout time.Duration) error {
	addr := net.JoinHostPort(peer.Ip, strconv.Itoa(int(peer.Port)))
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

	client := dfdaemonv2.NewDfdaemonUploadClient(conn)
	return p.retry.run(ctx, func() error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		if _, err := client.DeleteTask(ctx, deleteTaskRequest); err != nil && status.Code(err) != codes.NotFound {
			return fmt.Errorf("%w: failed to delete task %s: %w", ErrInternal, deleteTaskRequest.GetTaskId(), err)
		}

		return nil
	})
}

// deleteImage has the seed peer delete the tasks of the image, retrying a
// transient failure on the same seed peer. Each attempt runs under the timeout.
func (p *Proxy) deleteImage(ctx context.Context, peer *commonv2.Host, deleteImageRequest *dfdaemonv2.DeleteImageRequest, timeout time.Duration) (*dfdaemonv2.DeleteImageResponse, error) {
	addr := net.JoinHostPort(peer.Ip, strconv.Itoa(int(peer.Port)))
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(
			grpc.MaxCallRecvMsgSize(math.MaxInt32),
			grpc.MaxCallSendMsgSize(math.MaxInt32),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to connect to seed peer %s: %v", ErrInternal, addr, err)
	}
	defer conn.Close()

	var deleteImageResponse *dfdaemonv2.DeleteImageResponse
	client := dfdaemonv2.NewDfdaemonUploadClient(conn)
	if err := p.retry.run(ctx, func() error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		resp, err := client.DeleteImage(ctx, deleteImageRequest)
		if err != nil {
			return fmt.Errorf("%w: failed to delete image %s: %w", ErrInternal, deleteImageRequest.GetUrl(), err)
		}

		deleteImageResponse = resp
		return nil
	}); err != nil {
		return nil, err
	}

	return deleteImageResponse, nil
}
