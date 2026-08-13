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
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"

	"d7y.io/dragonfly-sdk/client-request/go/internal/oci"
)

// Preheat preheats a file by downloading it to the seed peers via the
// Dragonfly. It triggers the selected seed peers to download the file by the
// dfdaemon download task API, without streaming the file content back to the
// client.
func (p *Proxy) Preheat(ctx context.Context, req *PreheatRequest) error {
	// Generate task id for selecting seed peer.
	id, err := taskID(req.URL, req.PieceLength, req.Tag, req.Application, req.FilteredQueryParams, req.ContentForCalculatingTaskID, req.EnableTaskIDBasedBlobDigest)
	if err != nil {
		return fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
	}

	// Select seed peers for downloading.
	seedPeers, err := p.seedPeerSelector.Select(id, uint32(p.maxRetries))
	if err != nil {
		return fmt.Errorf("%w: failed to select seed peers from scheduler: %v", ErrInternal, err)
	}

	priority := commonv2.Priority_LEVEL6
	if req.Priority != nil {
		priority = commonv2.Priority(*req.Priority)
	}

	// Construct the download task request for preheating.
	download := &commonv2.Download{
		Url:                         req.URL,
		Type:                        commonv2.TaskType_STANDARD,
		Tag:                         optional(req.Tag),
		Application:                 optional(req.Application),
		Priority:                    priority,
		FilteredQueryParams:         req.FilteredQueryParams,
		RequestHeader:               headerToMap(req.Header),
		PieceLength:                 req.PieceLength,
		Timeout:                     durationpb.New(req.Timeout),
		ContentForCalculatingTaskId: optional(req.ContentForCalculatingTaskID),
		RemoteIp:                    optional(ip.IPv4.String()),
		EnableTaskIdBasedBlobDigest: req.EnableTaskIDBasedBlobDigest,
	}

	var lastErr error
	for _, peer := range seedPeers {
		// Trigger the seed peer to download the task and wait for the download
		// task to finish, without streaming the file content back to the client.
		if err := p.downloadTask(ctx, peer, id, download, req); err != nil {
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

// downloadTask triggers the seed peer to download the task and drains the
// response stream.
func (p *Proxy) downloadTask(ctx context.Context, peer *commonv2.Host, id string, download *commonv2.Download, req *PreheatRequest) error {
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

	ctx, cancel := context.WithTimeout(ctx, req.Timeout)
	defer cancel()

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
	result, err := oci.Resolve(ctx, &oci.ResolveOptions{
		Image:    req.Image,
		Username: req.Username,
		Password: req.Password,
		Platform: req.Platform,
	})
	if err != nil {
		return err
	}

	if result.Token == "" {
		return fmt.Errorf("%w: registry did not return authentication token", ErrInternal)
	}

	// Build authorization header for blob downloads through the Dragonfly.
	header := make(http.Header)
	header.Set("Authorization", result.Token)

	for _, blobURL := range result.BlobURLs {
		preheatReq := &PreheatRequest{
			URL:                         blobURL,
			Header:                      header.Clone(),
			PieceLength:                 req.PieceLength,
			Tag:                         req.Tag,
			Application:                 req.Application,
			FilteredQueryParams:         req.FilteredQueryParams,
			ContentForCalculatingTaskID: req.ContentForCalculatingTaskID,
			EnableTaskIDBasedBlobDigest: req.EnableTaskIDBasedBlobDigest,
			Priority:                    req.Priority,
			Timeout:                     req.Timeout,
			ClientCert:                  req.ClientCert,
		}

		if err := p.Preheat(ctx, preheatReq); err != nil {
			return err
		}
	}

	return nil
}

// optional returns a pointer to the string, or nil when it is empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}

	return &s
}
