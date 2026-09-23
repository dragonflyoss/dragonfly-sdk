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

// Delete deletes a preheated file from the replicas of seed peers via the
// Dragonfly. It has every replica seed peer delete the task by the dfdaemon
// delete task API, clamping the replicas to the available seed peers instead
// of failing when fewer are available.
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
	seedPeers, err := p.seedPeerSelector.Select(id, uint32(req.replicas))
	if err != nil {
		return fmt.Errorf("%w: failed to select seed peers from scheduler: %v", ErrInternal, err)
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
			return p.delete(ctx, peer, deleteTaskRequest, req.timeout)
		})
	}

	return g.Wait()
}

// DeleteImage deletes a preheated OCI image from the seed peers via the
// Dragonfly. It resolves the image like PreheatImage and has the replica seed
// peers serving each matched platform manifest and each blob (config and
// layers) delete the task by the dfdaemon delete task API. A seed peer
// answering NotFound for a task counts as deleted.
func (p *Proxy) DeleteImage(ctx context.Context, req *DeleteImageRequest) error {
	if err := req.validate(); err != nil {
		return err
	}

	ref, err := oci.ParseImage(req.image)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	manifestURLs, blobURLs, _, err := oci.Resolve(ctx, ref,
		oci.WithAuth(req.username, req.password),
		oci.WithPlatform(req.platform),
		oci.WithHTTPClient(oci.DefaultHTTPClient()),
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}

	// Delete the manifests and blobs concurrently, limited by the concurrent
	// task count.
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(req.concurrentTaskCount)
	for _, url := range uniqueURLs(slices.Concat(manifestURLs, blobURLs)) {
		g.Go(func() error {
			deleteReq := &DeleteRequest{
				url:                         url,
				pieceLength:                 req.pieceLength,
				tag:                         req.tag,
				application:                 req.application,
				filteredQueryParams:         req.filteredQueryParams,
				contentForCalculatingTaskID: req.contentForCalculatingTaskID,
				enableTaskIDBasedBlobDigest: req.enableTaskIDBasedBlobDigest,
				replicas:                    req.replicas,
				timeout:                     req.timeout,
			}

			return p.Delete(ctx, deleteReq)
		})
	}

	return g.Wait()
}

// delete has the seed peer delete the task, retrying a transient failure on
// the same seed peer. Each attempt runs under the timeout. A NotFound answer
// counts as deleted, aligned with the delete task job of the scheduler.
func (p *Proxy) delete(ctx context.Context, peer *commonv2.Host, deleteTaskRequest *dfdaemonv2.DeleteTaskRequest, timeout time.Duration) error {
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
	_, err = retry(ctx, p.retry, func() (struct{}, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

		if _, err := client.DeleteTask(ctx, deleteTaskRequest); err != nil && status.Code(err) != codes.NotFound {
			return struct{}{}, fmt.Errorf("%w: failed to delete task %s: %w", ErrInternal, deleteTaskRequest.GetTaskId(), err)
		}

		return struct{}{}, nil
	})

	return err
}
