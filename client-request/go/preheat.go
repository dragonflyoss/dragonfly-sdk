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
	"github.com/containerd/platforms"
	"github.com/distribution/reference"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"
)

// dockerRegistryHost is the actual registry host for docker.io references.
const dockerRegistryHost = "registry-1.docker.io"

// Preheat preheats a file by downloading it to the seed peers via the Dragonfly.
// It triggers the selected seed peers to download the file by the dfdaemon
// download task API, without streaming the file content back to the client.
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
	remoteIP := ip.IPv4.String()
	download := &commonv2.Download{
		Url:                         req.URL,
		Type:                        commonv2.TaskType_STANDARD,
		Priority:                    priority,
		FilteredQueryParams:         req.FilteredQueryParams,
		RequestHeader:               headerToMap(req.Header),
		PieceLength:                 req.PieceLength,
		Timeout:                     durationpb.New(req.Timeout),
		RemoteIp:                    &remoteIP,
		EnableTaskIdBasedBlobDigest: req.EnableTaskIDBasedBlobDigest,
	}

	if req.Tag != "" {
		download.Tag = &req.Tag
	}

	if req.Application != "" {
		download.Application = &req.Application
	}

	if req.ContentForCalculatingTaskID != "" {
		download.ContentForCalculatingTaskId = &req.ContentForCalculatingTaskID
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

// downloadTask triggers the seed peer to download the task and drains the response stream.
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
	ref, err := parseImage(req.Image)
	if err != nil {
		return err
	}

	httpClient := &http.Client{
		Timeout:   req.Timeout,
		Transport: http.DefaultTransport.(*http.Transport).Clone(),
	}

	client, err := oci.NewAuthClient(ref, httpClient, req.Username, req.Password)
	if err != nil {
		return fmt.Errorf("%w: failed to authenticate with registry: %v", ErrInternal, err)
	}

	platform := platforms.DefaultSpec()
	if req.Platform != "" {
		platform, err = platforms.Parse(req.Platform)
		if err != nil {
			return fmt.Errorf("%w: invalid platform format %q, expected \"os/arch\" (e.g., \"linux/amd64\"): %v", ErrInvalidArgument, req.Platform, err)
		}
	}

	manifests, err := client.ResolveManifests(ctx, ref, make(http.Header), platform)
	if err != nil {
		return fmt.Errorf("%w: failed to pull image manifest: %v", ErrInternal, err)
	}

	if len(manifests) == 0 {
		return fmt.Errorf("%w: no matching manifest for platform %s", ErrInternal, platforms.Format(platform))
	}

	token := client.AuthToken()
	if token == "" {
		return fmt.Errorf("%w: registry did not return authentication token", ErrInternal)
	}

	// Build authorization header for blob downloads through the Dragonfly.
	header := make(http.Header)
	header.Set("Authorization", token)

	for _, manifest := range manifests {
		for _, desc := range manifest.References() {
			preheatReq := &PreheatRequest{
				URL:                         ref.BlobURL(desc.Digest.String()),
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
	}

	return nil
}

// parseImage parses an image reference (e.g., "docker.io/library/nginx:latest")
// into a registry reference. The reference is normalized with docker
// conventions, so "nginx:latest" resolves to "docker.io/library/nginx:latest".
func parseImage(image string) (*oci.Reference, error) {
	named, err := reference.ParseNormalizedNamed(image)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid image reference: %v", ErrInvalidArgument, err)
	}
	named = reference.TagNameOnly(named)

	var tag string
	switch ref := named.(type) {
	case reference.Digested:
		tag = ref.Digest().String()
	case reference.Tagged:
		tag = ref.Tag()
	default:
		return nil, fmt.Errorf("%w: invalid image reference: %s", ErrInvalidArgument, image)
	}

	registry := reference.Domain(named)
	if registry == "docker.io" {
		registry = dockerRegistryHost
	}

	return &oci.Reference{
		Scheme:     "https",
		Registry:   registry,
		Repository: reference.Path(named),
		Reference:  tag,
	}, nil
}
