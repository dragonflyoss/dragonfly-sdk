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
const defaultRequestTimeout = 30 * 60 * time.Second

// defaultConcurrentTaskCount is the default number of blobs to preheat concurrently.
const defaultConcurrentTaskCount = 4

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

	// GetWithEndpoint sends a GET request to a remote server via the given
	// seed peer endpoint of the Dragonfly (e.g., one returned by
	// LookupEndpoints), instead of selecting a seed peer by the consistent
	// hash ring. It returns a response with a streaming body and the caller
	// must close the body.
	GetWithEndpoint(ctx context.Context, endpoint string, req *GetRequest) (*GetResponse, error)

	// GetIntoWithEndpoint sends a GET request to a remote server via the
	// given seed peer endpoint of the Dragonfly and writes the response body
	// directly into the provided writer.
	GetIntoWithEndpoint(ctx context.Context, endpoint string, req *GetRequest, w io.Writer) (*GetResponse, error)

	// Preheat preheats a file by downloading it to the seed peers via the
	// Dragonfly, without streaming the file content back to the client.
	Preheat(ctx context.Context, req *PreheatRequest) error

	// PreheatImage preheats an OCI image by downloading all its blobs via the
	// Dragonfly. It resolves the image manifest (including multi-platform
	// image indexes) and triggers the seed client to download each blob.
	PreheatImage(ctx context.Context, req *PreheatImageRequest) error

	// LookupEndpoints looks up the endpoints of the seed peers serving the
	// request, in the consistent hash ring selection order for the request's
	// task id. The number of endpoints is limited by the max retries, and the
	// same seed peer may appear multiple times when it is selected for retries.
	LookupEndpoints(ctx context.Context, req *GetRequest) ([]string, error)
}

// GetRequest represents a GET request to be sent via the Dragonfly. Construct
// it with NewGetRequest and set the optional parameters with GetRequestOption.
type GetRequest struct {
	// url is the url of the request.
	url string

	// header is the headers of the request.
	header http.Header

	// pieceLength is the task piece length.
	pieceLength *uint64

	// tag identifies different tasks for the same url.
	tag string

	// application identifies different tasks for the same url.
	application string

	// filteredQueryParams is the filtered query params to generate the task id.
	filteredQueryParams []string

	// contentForCalculatingTaskID is the content for calculating the task id.
	contentForCalculatingTaskID string

	// enableTaskIDBasedBlobDigest indicates whether to use the blob digest for
	// task id calculation when downloading from OCI registries.
	enableTaskIDBasedBlobDigest bool

	// priority is the task priority.
	priority *int32

	// timeout is the timeout of the request.
	timeout time.Duration

	// certificates is the client certificates for the request.
	// TODO(chlins): Support client certificates.
	certificates []*x509.Certificate
}

// GetRequestOption configures the GetRequest.
type GetRequestOption func(r *GetRequest)

// WithGetRequestHeader sets the headers of the request.
func WithGetRequestHeader(header http.Header) GetRequestOption {
	return func(r *GetRequest) { r.header = header }
}

// WithGetRequestPieceLength sets the task piece length.
func WithGetRequestPieceLength(pieceLength uint64) GetRequestOption {
	return func(r *GetRequest) { r.pieceLength = &pieceLength }
}

// WithGetRequestTag sets the tag that identifies different tasks for the same url.
func WithGetRequestTag(tag string) GetRequestOption {
	return func(r *GetRequest) { r.tag = tag }
}

// WithGetRequestApplication sets the application that identifies different tasks for
// the same url.
func WithGetRequestApplication(application string) GetRequestOption {
	return func(r *GetRequest) { r.application = application }
}

// WithGetRequestFilteredQueryParams sets the filtered query params to generate the
// task id. When the filter is ["Signature", "Expires", "ns"], for example:
// http://example.com/xyz?Expires=e1&Signature=s1&ns=docker.io and
// http://example.com/xyz?Expires=e2&Signature=s2&ns=docker.io
// will generate the same task id.
func WithGetRequestFilteredQueryParams(params []string) GetRequestOption {
	return func(r *GetRequest) { r.filteredQueryParams = params }
}

// WithGetRequestContentForCalculatingTaskID sets the content for calculating the task
// id. This is used when the task id cannot be calculated based on the url and
// other parameters, such as when the url contains dynamic query parameters
// that cannot be filtered out.
func WithGetRequestContentForCalculatingTaskID(content string) GetRequestOption {
	return func(r *GetRequest) { r.contentForCalculatingTaskID = content }
}

// WithGetRequestEnableTaskIDBasedBlobDigest sets whether to use the blob digest for
// task id calculation when downloading from OCI registries. When enabled for
// OCI blob urls (e.g., /v2/<name>/blobs/sha256:<digest>), the task id is
// derived from the blob digest rather than the full url. This enables
// deduplication across registries.
func WithGetRequestEnableTaskIDBasedBlobDigest(enable bool) GetRequestOption {
	return func(r *GetRequest) { r.enableTaskIDBasedBlobDigest = enable }
}

// WithGetRequestPriority sets the task priority, refer to
// https://github.com/dragonflyoss/api/blob/main/proto/common.proto#L67.
func WithGetRequestPriority(priority int32) GetRequestOption {
	return func(r *GetRequest) { r.priority = &priority }
}

// WithGetRequestTimeout sets the timeout of the request.
func WithGetRequestTimeout(timeout time.Duration) GetRequestOption {
	return func(r *GetRequest) { r.timeout = timeout }
}

// WithGetRequestCertificates sets the client certificates for the request.
// TODO(chlins): Support client certificates.
func WithGetRequestCertificates(certs []*x509.Certificate) GetRequestOption {
	return func(r *GetRequest) { r.certificates = certs }
}

// NewGetRequest returns a GetRequest for the url with default values: the
// default filtered query params, blob digest based task id enabled and a 600s
// timeout.
func NewGetRequest(url string, opts ...GetRequestOption) *GetRequest {
	r := &GetRequest{
		url:                         url,
		header:                      make(http.Header),
		filteredQueryParams:         idgen.DefaultFilteredQueryParams,
		enableTaskIDBasedBlobDigest: true,
		timeout:                     defaultRequestTimeout,
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
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
// Construct it with NewPreheatRequest and set the optional parameters with
// PreheatRequestOption.
type PreheatRequest struct {
	// url is the url of the request.
	url string

	// header is the headers of the request.
	header http.Header

	// pieceLength is the task piece length.
	pieceLength *uint64

	// tag identifies different tasks for the same url.
	tag string

	// application identifies different tasks for the same url.
	application string

	// filteredQueryParams is the filtered query params to generate the task id.
	filteredQueryParams []string

	// contentForCalculatingTaskID is the content for calculating the task id.
	contentForCalculatingTaskID string

	// enableTaskIDBasedBlobDigest indicates whether to use the blob digest for
	// task id calculation when downloading from OCI registries.
	enableTaskIDBasedBlobDigest bool

	// priority is the task priority.
	priority *int32

	// timeout is the timeout of the request.
	timeout time.Duration

	// certificates is the client certificates for the request.
	// TODO(chlins): Support client certificates.
	certificates []*x509.Certificate
}

// PreheatRequestOption configures the PreheatRequest.
type PreheatRequestOption func(r *PreheatRequest)

// WithPreheatRequestHeader sets the headers of the request.
func WithPreheatRequestHeader(header http.Header) PreheatRequestOption {
	return func(r *PreheatRequest) { r.header = header }
}

// WithPreheatRequestPieceLength sets the task piece length.
func WithPreheatRequestPieceLength(pieceLength uint64) PreheatRequestOption {
	return func(r *PreheatRequest) { r.pieceLength = &pieceLength }
}

// WithPreheatRequestTag sets the tag that identifies different tasks for the same
// url.
func WithPreheatRequestTag(tag string) PreheatRequestOption {
	return func(r *PreheatRequest) { r.tag = tag }
}

// WithPreheatRequestApplication sets the application that identifies different tasks
// for the same url.
func WithPreheatRequestApplication(application string) PreheatRequestOption {
	return func(r *PreheatRequest) { r.application = application }
}

// WithPreheatRequestFilteredQueryParams sets the filtered query params to generate
// the task id.
func WithPreheatRequestFilteredQueryParams(params []string) PreheatRequestOption {
	return func(r *PreheatRequest) { r.filteredQueryParams = params }
}

// WithPreheatRequestContentForCalculatingTaskID sets the content for calculating the
// task id.
func WithPreheatRequestContentForCalculatingTaskID(content string) PreheatRequestOption {
	return func(r *PreheatRequest) { r.contentForCalculatingTaskID = content }
}

// WithPreheatRequestEnableTaskIDBasedBlobDigest sets whether to use the blob digest
// for task id calculation when downloading from OCI registries.
func WithPreheatRequestEnableTaskIDBasedBlobDigest(enable bool) PreheatRequestOption {
	return func(r *PreheatRequest) { r.enableTaskIDBasedBlobDigest = enable }
}

// WithPreheatRequestPriority sets the task priority.
func WithPreheatRequestPriority(priority int32) PreheatRequestOption {
	return func(r *PreheatRequest) { r.priority = &priority }
}

// WithPreheatRequestTimeout sets the timeout of the request.
func WithPreheatRequestTimeout(timeout time.Duration) PreheatRequestOption {
	return func(r *PreheatRequest) { r.timeout = timeout }
}

// WithPreheatRequestCertificates sets the client certificates for the request.
// TODO(chlins): Support client certificates.
func WithPreheatRequestCertificates(certs []*x509.Certificate) PreheatRequestOption {
	return func(r *PreheatRequest) { r.certificates = certs }
}

// NewPreheatRequest returns a PreheatRequest for the url with default values.
func NewPreheatRequest(url string, opts ...PreheatRequestOption) *PreheatRequest {
	r := &PreheatRequest{
		url:                         url,
		header:                      make(http.Header),
		filteredQueryParams:         idgen.DefaultFilteredQueryParams,
		enableTaskIDBasedBlobDigest: true,
		timeout:                     defaultRequestTimeout,
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
}

// PreheatImageRequest represents a request to preheat an OCI image through
// the Dragonfly seed client. The preheat downloads all blobs (config and
// layers) of the specified image via the Dragonfly proxy. Construct it with
// NewPreheatImageRequest and set the optional parameters with
// PreheatImageRequestOption.
type PreheatImageRequest struct {
	// image is the OCI image reference (e.g., "docker.io/library/nginx:latest").
	image string

	// username is the username for registry authentication.
	username string

	// password is the password for registry authentication.
	password string

	// platform specifies the target platform in the format "os/arch".
	platform string

	// pieceLength is the task piece length.
	pieceLength *uint64

	// tag identifies different tasks for the same url.
	tag string

	// application identifies different tasks for the same url.
	application string

	// filteredQueryParams is the filtered query params to generate the task id.
	filteredQueryParams []string

	// contentForCalculatingTaskID is the content for calculating the task id.
	contentForCalculatingTaskID string

	// enableTaskIDBasedBlobDigest indicates whether to use the blob digest for
	// task id calculation when downloading from OCI registries.
	enableTaskIDBasedBlobDigest bool

	// priority is the task priority.
	priority *int32

	// timeout is the timeout for each blob download request.
	timeout time.Duration

	// concurrentTaskCount is the number of blobs to preheat concurrently.
	concurrentTaskCount int

	// certificates is the client certificates for the request.
	// TODO(chlins): Support client certificates.
	certificates []*x509.Certificate
}

// PreheatImageRequestOption configures the PreheatImageRequest.
type PreheatImageRequestOption func(r *PreheatImageRequest)

// WithPreheatImageRequestAuth sets the username and password for registry
// authentication. If not provided, anonymous access is used.
func WithPreheatImageRequestAuth(username, password string) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) {
		r.username = username
		r.password = password
	}
}

// WithPreheatImageRequestPlatform sets the target platform in the format "os/arch"
// (e.g., "linux/amd64", "linux/arm64"). This is used to select the correct
// manifest from a multi-platform image index, default is current platform.
func WithPreheatImageRequestPlatform(platform string) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.platform = platform }
}

// WithPreheatImageRequestPieceLength sets the task piece length.
func WithPreheatImageRequestPieceLength(pieceLength uint64) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.pieceLength = &pieceLength }
}

// WithPreheatImageRequestTag sets the tag that identifies different tasks for the
// same url.
func WithPreheatImageRequestTag(tag string) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.tag = tag }
}

// WithPreheatImageRequestApplication sets the application that identifies different
// tasks for the same url.
func WithPreheatImageRequestApplication(application string) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.application = application }
}

// WithPreheatImageRequestFilteredQueryParams sets the filtered query params to
// generate the task id.
func WithPreheatImageRequestFilteredQueryParams(params []string) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.filteredQueryParams = params }
}

// WithPreheatImageRequestContentForCalculatingTaskID sets the content for calculating
// the task id.
func WithPreheatImageRequestContentForCalculatingTaskID(content string) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.contentForCalculatingTaskID = content }
}

// WithPreheatImageRequestEnableTaskIDBasedBlobDigest sets whether to use the blob
// digest for task id calculation when downloading from OCI registries.
func WithPreheatImageRequestEnableTaskIDBasedBlobDigest(enable bool) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.enableTaskIDBasedBlobDigest = enable }
}

// WithPreheatImageRequestPriority sets the task priority.
func WithPreheatImageRequestPriority(priority int32) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.priority = &priority }
}

// WithPreheatImageRequestTimeout sets the timeout for each blob download request.
func WithPreheatImageRequestTimeout(timeout time.Duration) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.timeout = timeout }
}

// WithPreheatImageRequestConcurrentTaskCount sets the number of blobs to
// preheat concurrently, default is 4.
func WithPreheatImageRequestConcurrentTaskCount(count int) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.concurrentTaskCount = count }
}

// WithPreheatImageRequestCertificates sets the client certificates for the request.
// TODO(chlins): Support client certificates.
func WithPreheatImageRequestCertificates(certs []*x509.Certificate) PreheatImageRequestOption {
	return func(r *PreheatImageRequest) { r.certificates = certs }
}

// NewPreheatImageRequest returns a PreheatImageRequest for the image with
// default values.
func NewPreheatImageRequest(image string, opts ...PreheatImageRequestOption) *PreheatImageRequest {
	r := &PreheatImageRequest{
		image:                       image,
		filteredQueryParams:         idgen.DefaultFilteredQueryParams,
		enableTaskIDBasedBlobDigest: true,
		timeout:                     defaultRequestTimeout,
		concurrentTaskCount:         defaultConcurrentTaskCount,
	}
	for _, opt := range opts {
		opt(r)
	}

	return r
}
