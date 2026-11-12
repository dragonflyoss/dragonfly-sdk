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
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	errordetailsv2 "d7y.io/api/v2/pkg/apis/errordetails/v2"
	"d7y.io/dragonfly/v2/pkg/idgen"
	nethttp "d7y.io/dragonfly/v2/pkg/net/http"
	"d7y.io/dragonfly/v2/pkg/net/ip"
	"d7y.io/dragonfly/v2/pkg/oci"
	"github.com/docker/distribution"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"
)

// grpcStatusDetailsBinHeader is the trailer of the details of the gRPC status,
// the same header as Status::GRPC_STATUS_DETAILS of tonic.
const grpcStatusDetailsBinHeader = "grpc-status-details-bin"

// Preheat preheats a file by downloading it to the replicas of seed peers, or
// to all seed peers with ScopeAllSeedPeers, via the Dragonfly. It triggers
// every selected seed peer to download the file by the dfdaemon download task
// API, without streaming the file content back to the client. It fails when
// the available seed peers are fewer than the replicas of the request.
func (p *Proxy) Preheat(ctx context.Context, req *PreheatRequest) error {
	if err := req.validate(); err != nil {
		return err
	}

	// Generate task id for selecting seed peer.
	id, err := idgen.TaskIDV2(req.url, req.pieceLength, req.tag, req.application, req.filteredQueryParams, req.contentForCalculatingTaskID, req.enableTaskIDBasedBlobDigest)
	if err != nil {
		return fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
	}

	// Select seed peers for downloading.
	seedPeers, err := p.selectSeedPeers(id, req.scope, req.replicas)
	if err != nil {
		return fmt.Errorf("%w: failed to select seed peers: %v", ErrInternal, err)
	}

	if req.scope == ScopeDefault && len(seedPeers) < req.replicas {
		return fmt.Errorf("%w: insufficient seed peers for %d replicas, %d available", ErrInternal, req.replicas, len(seedPeers))
	}

	priority := commonv2.Priority_LEVEL6
	if req.priority != nil {
		priority = commonv2.Priority(*req.priority)
	}

	// Construct the download task request for preheating.
	remoteIP := ip.IPv4.String()
	download := &commonv2.Download{
		Url:                         req.url,
		Type:                        commonv2.TaskType_STANDARD,
		Priority:                    priority,
		FilteredQueryParams:         req.filteredQueryParams,
		RequestHeader:               headerToMap(req.header),
		PieceLength:                 req.pieceLength,
		Timeout:                     durationpb.New(req.timeout),
		RemoteIp:                    &remoteIP,
		EnableTaskIdBasedBlobDigest: req.enableTaskIDBasedBlobDigest,
		SchedulingPolicy:            commonv2.SchedulingPolicy_ALWAYS,
	}

	if req.tag != "" {
		download.Tag = &req.tag
	}

	if req.application != "" {
		download.Application = &req.application
	}

	if req.contentForCalculatingTaskID != "" {
		download.ContentForCalculatingTaskId = &req.contentForCalculatingTaskID
	}

	downloadTaskRequest := &dfdaemonv2.DownloadTaskRequest{Download: download}

	// Trigger every replica seed peer to download the task concurrently and
	// wait for the download tasks to finish. A transient failure is retried on
	// the same seed peer, so the file lands on every replica.
	g, ctx := errgroup.WithContext(ctx)
	for _, peer := range seedPeers {
		g.Go(func() error {
			return p.downloadTask(ctx, peer, id, downloadTaskRequest, req.timeout)
		})
	}

	return g.Wait()
}

// PreheatImage preheats an OCI image via the Dragonfly. It resolves the
// manifests of all the platforms, or of the specified one, and has the seed
// peers download each manifest and blob. The seed peers record the tasks by the
// image and the platform for DeleteImage.
func (p *Proxy) PreheatImage(ctx context.Context, req *PreheatImageRequest) error {
	if err := req.validate(); err != nil {
		return err
	}

	ref, err := oci.ParseImage(req.image)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
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
			return cause
		}

		return fmt.Errorf("%w: %v", ErrInternal, err)
	}

	// Authorize the targets with one client, which keeps the token of the
	// registry and renews it when it expires during a long preheat.
	authClient, err := oci.NewAuthClient(ref, p.registryClient, req.username, req.password)
	if err != nil {
		return fmt.Errorf("%w: failed to authenticate with registry: %v", ErrInternal, err)
	}

	// Build the accept header with the manifest media types for downloading
	// manifests, so the registry returns the exact representation matching the
	// digest.
	header := make(http.Header)
	manifestHeader := make(http.Header)
	manifestHeader.Set("Accept", strings.Join(distribution.ManifestMediaTypes(), ","))

	// Collect the manifests and blobs of the matched platforms. A blob shared
	// by several platforms is preheated for each of them.
	type target struct {
		url    string
		header http.Header
		image  *commonv2.Image
	}

	var targets []target
	for _, manifest := range manifests {
		image := &commonv2.Image{Reference: ref.String()}
		if manifest.Platform != "" {
			image.Platform = &manifest.Platform
		}

		if slices.ContainsFunc(targets, func(t target) bool {
			return t.url == manifest.URL && t.image.GetPlatform() == manifest.Platform
		}) {
			continue
		}

		targets = append(targets, target{url: manifest.URL, header: manifestHeader, image: image})
		for _, blobURL := range manifest.BlobURLs {
			if !slices.ContainsFunc(targets, func(t target) bool {
				return t.url == blobURL && t.image.GetPlatform() == manifest.Platform
			}) {
				targets = append(targets, target{url: blobURL, header: header, image: image})
			}
		}
	}

	priority := commonv2.Priority_LEVEL6
	if req.priority != nil {
		priority = commonv2.Priority(*req.priority)
	}

	// Preheat the manifests and blobs concurrently, limited by the concurrent
	// task count.
	remoteIP := ip.IPv4.String()
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(req.concurrentTaskCount)
	for _, target := range targets {
		g.Go(func() error {
			// Generate task id for selecting seed peer.
			id, err := idgen.TaskIDV2(target.url, req.pieceLength, req.tag, req.application, req.filteredQueryParams, "", req.enableTaskIDBasedBlobDigest)
			if err != nil {
				return fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
			}

			// Select seed peers for downloading.
			seedPeers, err := p.selectSeedPeers(id, req.scope, req.replicas)
			if err != nil {
				return fmt.Errorf("%w: failed to select seed peers: %v", ErrInternal, err)
			}

			if req.scope == ScopeDefault && len(seedPeers) < req.replicas {
				return fmt.Errorf("%w: insufficient seed peers for %d replicas, %d available", ErrInternal, req.replicas, len(seedPeers))
			}

			// Authorize the target when it starts, the registry may need no authorization.
			authorization, err := authClient.Authorization()
			if err != nil {
				return fmt.Errorf("%w: failed to authenticate with registry: %v", ErrInternal, err)
			}

			requestHeader := target.header.Clone()
			if authorization != "" {
				requestHeader.Set("Authorization", authorization)
			}

			// Construct the download task request with the image.
			download := &commonv2.Download{
				Url:                         target.url,
				Type:                        commonv2.TaskType_STANDARD,
				Priority:                    priority,
				FilteredQueryParams:         req.filteredQueryParams,
				RequestHeader:               headerToMap(requestHeader),
				PieceLength:                 req.pieceLength,
				Timeout:                     durationpb.New(req.timeout),
				RemoteIp:                    &remoteIP,
				EnableTaskIdBasedBlobDigest: req.enableTaskIDBasedBlobDigest,
				SchedulingPolicy:            commonv2.SchedulingPolicy_ALWAYS,
				Image:                       target.image,
			}

			if req.tag != "" {
				download.Tag = &req.tag
			}

			if req.application != "" {
				download.Application = &req.application
			}

			downloadTaskRequest := &dfdaemonv2.DownloadTaskRequest{Download: download}

			// Trigger every replica seed peer to download the task concurrently.
			g, ctx := errgroup.WithContext(ctx)
			for _, peer := range seedPeers {
				g.Go(func() error {
					return p.downloadTask(ctx, peer, id, downloadTaskRequest, req.timeout)
				})
			}

			return g.Wait()
		})
	}

	return g.Wait()
}

// downloadTask has the seed peer download the task and drains the response
// stream, retrying a transient failure on the same seed peer so the file lands
// on every replica. Each attempt connects to the seed peer and runs under the
// timeout. The gRPC status of a failure stays in the error, so isRetryable can
// tell a definitive one apart.
func (p *Proxy) downloadTask(ctx context.Context, peer *commonv2.Host, id string, downloadTaskRequest *dfdaemonv2.DownloadTaskRequest, timeout time.Duration) error {
	addr := net.JoinHostPort(peer.Ip, strconv.Itoa(int(peer.Port)))
	return p.retry.run(ctx, func() error {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()

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

		stream, err := dfdaemonv2.NewDfdaemonUploadClient(conn).DownloadTask(ctx, downloadTaskRequest)
		if err != nil {
			return fmt.Errorf("%w: failed to download task %s: %w", ErrInternal, id, err)
		}

		for {
			if _, err := stream.Recv(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}

				// The dfdaemon reports a failure of the source as the JSON of the
				// backend error in the details of the status. The Rust SDK reads
				// them by Status::details of tonic, but grpc-go only decodes the
				// details of google.rpc.Status and leaves the others in the
				// trailer. Read the trailer to keep the same behavior as the Rust
				// SDK, so a definitive answer such as 404 is not retried.
				var backend errordetailsv2.Backend
				details := stream.Trailer().Get(grpcStatusDetailsBinHeader)
				if len(details) != 1 || json.Unmarshal([]byte(details[0]), &backend) != nil || backend.StatusCode == nil {
					return fmt.Errorf("%w: failed to download task %s: %w", ErrInternal, id, err)
				}

				return &BackendError{Message: backend.GetMessage(), Header: nethttp.MapToHeader(backend.GetHeader()), StatusCode: int(backend.GetStatusCode())}
			}
		}
	})
}
