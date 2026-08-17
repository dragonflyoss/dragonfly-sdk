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
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	dfdaemonv2 "d7y.io/api/v2/pkg/apis/dfdaemon/v2"
	"d7y.io/dragonfly/v2/pkg/net/ip"
	"d7y.io/dragonfly/v2/pkg/oci"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"
)

// Preheat preheats a file by downloading it to the seed peers via the Dragonfly.
// It triggers the selected seed peers to download the file by the dfdaemon
// download task API, without streaming the file content back to the client.
func (p *Proxy) Preheat(ctx context.Context, req *PreheatRequest) error {
	ctx, cancel := context.WithTimeout(ctx, req.timeout)
	defer cancel()

	// Generate task id for selecting seed peer. PreheatRequest has the same
	// fields as GetRequest, so it converts directly.
	id, err := generateTaskID((*GetRequest)(req))
	if err != nil {
		return fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
	}

	// Select seed peers for downloading.
	seedPeers, err := p.seedPeerSelector.Select(id, uint32(p.maxRetries))
	if err != nil {
		return fmt.Errorf("%w: failed to select seed peers from scheduler: %v", ErrInternal, err)
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

	var lastErr error
	for _, peer := range seedPeers {
		// Trigger the seed peer to download the task and wait for the download
		// task to finish, without streaming the file content back to the client.
		if err := p.downloadTask(ctx, peer, id, download); err != nil {
			lastErr = err
			continue
		}

		return nil
	}

	if lastErr != nil {
		return lastErr
	}

	return fmt.Errorf("%w: failed to download task from any seed peer", ErrInternal)
}

// downloadTask triggers the seed peer to download the task and drains the response stream.
func (p *Proxy) downloadTask(ctx context.Context, peer *commonv2.Host, id string, download *commonv2.Download) error {
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

	stream, err := dfdaemonv2.NewDfdaemonUploadClient(conn).DownloadTask(ctx, &dfdaemonv2.DownloadTaskRequest{Download: download})
	if err != nil {
		return fmt.Errorf("%w: failed to download task %s: %v", ErrInternal, id, err)
	}

	for {
		if _, err := stream.Recv(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return fmt.Errorf("%w: failed to download task %s: %v", ErrInternal, id, err)
		}
	}
}

// PreheatImage preheats an OCI image by downloading all its blobs via the
// Dragonfly. It parses the image reference, authenticates with the OCI
// registry, resolves the image manifest (including multi-platform image
// indexes), and triggers the seed client to download each blob (config and
// layers), without streaming the blob content back to the client.
func (p *Proxy) PreheatImage(ctx context.Context, req *PreheatImageRequest) error {
	ref, err := oci.ParseImage(req.image)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}

	// Resolve the image manifests (including multi-platform image indexes) and
	// collect the blob urls along with the authorization token.
	blobURLs, token, err := oci.Resolve(ctx, ref,
		oci.WithAuth(req.username, req.password),
		oci.WithPlatform(req.platform),
		oci.WithHTTPClient(oci.DefaultHTTPClient()),
	)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInternal, err)
	}

	if token == "" {
		return fmt.Errorf("%w: registry did not return authentication token", ErrInternal)
	}

	// Build authorization header for blob downloads through the Dragonfly.
	header := make(http.Header)
	header.Set("Authorization", token)
	for _, blobURL := range blobURLs {
		preheatReq := &PreheatRequest{
			url:                         blobURL,
			header:                      header.Clone(),
			pieceLength:                 req.pieceLength,
			tag:                         req.tag,
			application:                 req.application,
			filteredQueryParams:         req.filteredQueryParams,
			contentForCalculatingTaskID: req.contentForCalculatingTaskID,
			enableTaskIDBasedBlobDigest: req.enableTaskIDBasedBlobDigest,
			priority:                    req.priority,
			timeout:                     req.timeout,
			certificates:                req.certificates,
		}

		if err := p.Preheat(ctx, preheatReq); err != nil {
			return err
		}
	}

	return nil
}

// headerToMap converts an http.Header to a map, the last value wins for
// duplicate keys.
func headerToMap(header http.Header) map[string]string {
	m := make(map[string]string, len(header))
	for k, v := range header {
		if len(v) > 0 {
			m[k] = v[len(v)-1]
		}
	}

	return m
}
