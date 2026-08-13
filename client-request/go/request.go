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

// Package request sends requests to remote servers via the Dragonfly
// P2P network. It is the Go implementation of the Rust crate
// dragonfly-client-request and generates identical task ids and seed peer
// selections.
package request

import (
	"context"
	"crypto/x509"
	"io"
	"net/http"
	"time"

	"d7y.io/dragonfly/v2/pkg/idgen"
)

// defaultRequestTimeout is the default timeout for requests.
const defaultRequestTimeout = 300 * time.Second

// Request is the interface for sending requests via the Dragonfly.
//
// It enables interaction with remote servers through the Dragonfly, shielding
// the complex request logic between the client and the Dragonfly seed client's
// proxy.
type Request interface {
	// Get sends a GET request to a remote server via the Dragonfly and returns
	// a response with a streaming body. The caller must close the body.
	Get(ctx context.Context, req *GetRequest) (*GetResponse, error)

	// GetInto sends a GET request to a remote server via the Dragonfly and
	// writes the response body directly into the provided writer.
	GetInto(ctx context.Context, req *GetRequest, w io.Writer) (*GetResponse, error)

	// Preheat preheats a file by downloading it to the seed peers via the
	// Dragonfly, without streaming the file content back to the client.
	Preheat(ctx context.Context, req *PreheatRequest) error

	// PreheatImage preheats an OCI image by downloading all its blobs via the
	// Dragonfly. It resolves the image manifest (including multi-platform
	// image indexes) and triggers the seed client to download each blob.
	PreheatImage(ctx context.Context, req *PreheatImageRequest) error
}

// GetRequest represents a GET request to be sent via the Dragonfly.
type GetRequest struct {
	// URL is the url of the request.
	URL string

	// Header is the headers of the request.
	Header http.Header

	// PieceLength is the task piece length.
	PieceLength *uint64

	// Tag identifies different tasks for the same url.
	Tag string

	// Application identifies different tasks for the same url.
	Application string

	// FilteredQueryParams is the filtered query params to generate the task id.
	// When the filter is ["Signature", "Expires", "ns"], for example:
	// http://example.com/xyz?Expires=e1&Signature=s1&ns=docker.io and
	// http://example.com/xyz?Expires=e2&Signature=s2&ns=docker.io
	// will generate the same task id.
	FilteredQueryParams []string

	// ContentForCalculatingTaskID is the content for calculating the task id.
	// This is used when the task id cannot be calculated based on the url and
	// other parameters, such as when the url contains dynamic query parameters
	// that cannot be filtered out.
	ContentForCalculatingTaskID string

	// EnableTaskIDBasedBlobDigest indicates whether to use the blob digest for
	// task id calculation when downloading from OCI registries. When enabled
	// for OCI blob urls (e.g., /v2/<name>/blobs/sha256:<digest>), the task id
	// is derived from the blob digest rather than the full url. This enables
	// deduplication across registries.
	EnableTaskIDBasedBlobDigest bool

	// Priority is the task priority, refer to
	// https://github.com/dragonflyoss/api/blob/main/proto/common.proto#L67.
	Priority *int32

	// Timeout is the timeout of the request.
	Timeout time.Duration

	// ClientCert is the client certificates for the request.
	// TODO(chlins): Support client certificates.
	ClientCert []*x509.Certificate
}

// NewGetRequest returns a GetRequest for the url with default values: the
// default filtered query params, blob digest based task id enabled and a 300s
// timeout.
func NewGetRequest(url string) *GetRequest {
	return &GetRequest{
		URL:                         url,
		Header:                      make(http.Header),
		FilteredQueryParams:         idgen.DefaultFilteredQueryParams,
		EnableTaskIDBasedBlobDigest: true,
		Timeout:                     defaultRequestTimeout,
	}
}

// GetResponse represents a GET response received via the Dragonfly.
type GetResponse struct {
	// Success indicates whether the response is successful.
	Success bool

	// Header is the headers of the response.
	Header http.Header

	// StatusCode is the status code of the response.
	StatusCode int

	// Body is the content of the response. It is nil for GetInto and must be
	// closed by the caller for Get.
	Body io.ReadCloser
}

// PreheatRequest represents a request to preheat a file through the Dragonfly
// seed client. The preheat downloads the specified file via the Dragonfly
// proxy, effectively caching it in the P2P network for faster downloading.
type PreheatRequest struct {
	// URL is the url of the request.
	URL string

	// Header is the headers of the request.
	Header http.Header

	// PieceLength is the task piece length.
	PieceLength *uint64

	// Tag identifies different tasks for the same url.
	Tag string

	// Application identifies different tasks for the same url.
	Application string

	// FilteredQueryParams is the filtered query params to generate the task id.
	FilteredQueryParams []string

	// ContentForCalculatingTaskID is the content for calculating the task id.
	ContentForCalculatingTaskID string

	// EnableTaskIDBasedBlobDigest indicates whether to use the blob digest for
	// task id calculation when downloading from OCI registries.
	EnableTaskIDBasedBlobDigest bool

	// Priority is the task priority.
	Priority *int32

	// Timeout is the timeout of the request.
	Timeout time.Duration

	// ClientCert is the client certificates for the request.
	// TODO(chlins): Support client certificates.
	ClientCert []*x509.Certificate
}

// NewPreheatRequest returns a PreheatRequest for the url with default values.
func NewPreheatRequest(url string) *PreheatRequest {
	return &PreheatRequest{
		URL:                         url,
		Header:                      make(http.Header),
		FilteredQueryParams:         idgen.DefaultFilteredQueryParams,
		EnableTaskIDBasedBlobDigest: true,
		Timeout:                     defaultRequestTimeout,
	}
}

// PreheatImageRequest represents a request to preheat an OCI image through
// the Dragonfly seed client. The preheat downloads all blobs (config and
// layers) of the specified image via the Dragonfly proxy.
type PreheatImageRequest struct {
	// Image is the OCI image reference (e.g., "docker.io/library/nginx:latest").
	Image string

	// Username is the username for registry authentication. If not provided,
	// anonymous access is used.
	Username string

	// Password is the password for registry authentication. If not provided,
	// anonymous access is used.
	Password string

	// Platform specifies the target platform in the format "os/arch"
	// (e.g., "linux/amd64", "linux/arm64"). This is used to select the correct
	// manifest from a multi-platform image index, default is current platform.
	Platform string

	// PieceLength is the optional piece length for the Dragonfly task.
	PieceLength *uint64

	// Tag identifies different tasks for the same url.
	Tag string

	// Application identifies different tasks for the same url.
	Application string

	// FilteredQueryParams is the filtered query params to generate the task id.
	FilteredQueryParams []string

	// ContentForCalculatingTaskID is the content for calculating the task id.
	ContentForCalculatingTaskID string

	// EnableTaskIDBasedBlobDigest indicates whether to use the blob digest for
	// task id calculation when downloading from OCI registries.
	EnableTaskIDBasedBlobDigest bool

	// Priority is the task priority.
	Priority *int32

	// Timeout is the timeout for each blob download request.
	Timeout time.Duration

	// ClientCert is the client certificates for the request.
	// TODO(chlins): Support client certificates.
	ClientCert []*x509.Certificate
}

// NewPreheatImageRequest returns a PreheatImageRequest for the image with
// default values.
func NewPreheatImageRequest(image string) *PreheatImageRequest {
	return &PreheatImageRequest{
		Image:                       image,
		FilteredQueryParams:         idgen.DefaultFilteredQueryParams,
		EnableTaskIDBasedBlobDigest: true,
		Timeout:                     defaultRequestTimeout,
	}
}
