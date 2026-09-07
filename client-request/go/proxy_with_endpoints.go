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
	"net/http"
	"slices"

	"github.com/cenkalti/backoff/v5"
)

// ProxyWithEndpointsOption configures the ProxyWithEndpoints.
type ProxyWithEndpointsOption func(p *ProxyWithEndpoints)

// WithProxyWithEndpointsMaxRetries sets the maximum number of retries of a
// request on a transient failure: a timeout, a connection or transport error,
// a dfdaemon error, or a 5xx or 408 answer. Each retry goes to the next
// endpoint, any other answer returns at once.
func WithProxyWithEndpointsMaxRetries(retries uint8) ProxyWithEndpointsOption {
	return func(p *ProxyWithEndpoints) { p.retry.maxRetries = retries }
}

// WithProxyWithEndpointsBackoff sets the exponential backoff between the retries. Without
// it retries go at once.
func WithProxyWithEndpointsBackoff(b *backoff.ExponentialBackOff) ProxyWithEndpointsOption {
	return func(p *ProxyWithEndpoints) { p.retry.backoff = b }
}

// Ensure ProxyWithEndpoints implements the RequestWithEndpoints interface at
// compile time.
var _ RequestWithEndpoints = (*ProxyWithEndpoints)(nil)

// ProxyWithEndpoints is the HTTP proxy client that sends requests via the
// fixed seed peer endpoints of the Dragonfly given at construction, without
// selecting seed peers by the consistent hash ring or syncing them from the
// scheduler.
type ProxyWithEndpoints struct {
	// retry is the retry policy of the requests.
	retry retryPolicy

	// endpoints is the seed peer endpoints serving the requests.
	endpoints []string

	// clients maps each endpoint to its own client, so every endpoint has its
	// own reusable connection pool.
	clients map[string]*http.Client
}

// NewWithEndpoints creates a ProxyWithEndpoints that sends requests via the
// given seed peer endpoints of the Dragonfly (e.g., the ones returned by
// Proxy.LookupEndpoints), e.g. "http://127.0.0.1:4001". Each endpoint gets
// its own client with a reusable connection pool.
func NewWithEndpoints(endpoints []string, opts ...ProxyWithEndpointsOption) (*ProxyWithEndpoints, error) {
	p := &ProxyWithEndpoints{
		retry:     defaultRetryPolicy,
		endpoints: slices.Clone(endpoints),
	}
	for _, opt := range opts {
		opt(p)
	}

	if len(p.endpoints) == 0 {
		return nil, fmt.Errorf("%w: endpoints must not be empty", ErrInvalidArgument)
	}

	if p.retry.maxRetries > 3 {
		return nil, fmt.Errorf("%w: max retries must be between 0 and 3", ErrInvalidArgument)
	}

	p.clients = make(map[string]*http.Client, len(p.endpoints))
	for _, endpoint := range p.endpoints {
		if _, ok := p.clients[endpoint]; ok {
			continue
		}

		client, err := httpClientFactory(endpoint)
		if err != nil {
			return nil, err
		}

		p.clients[endpoint] = client
	}

	return p, nil
}

// Get sends a GET request to a remote server via the seed peer endpoints of
// the Dragonfly and returns a response with a streaming body. The request is
// sent to a randomly picked endpoint and a transient failure is retried on
// the others up to the max retries. The caller must close the body.
func (p *ProxyWithEndpoints) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	resp, cancel, err := p.trySend(ctx, req)
	if err != nil {
		return nil, err
	}

	return streamResponse(resp, cancel), nil
}

// GetInto sends a GET request to a remote server via the seed peer endpoints
// of the Dragonfly and writes the response body directly into the provided
// writer. The request is sent to a randomly picked endpoint and a transient
// failure is retried on the others up to the max retries, while a failed body
// read is not, since the writer cannot be rewound.
func (p *ProxyWithEndpoints) GetInto(ctx context.Context, req *GetRequest, w io.Writer) (*GetResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	resp, cancel, err := p.trySend(ctx, req)
	if err != nil {
		return nil, err
	}
	defer cancel()

	return copyResponse(resp, w)
}

// trySend scatters the request across the endpoints, retrying a transient
// failure on the next endpoint up to the max retries.
func (p *ProxyWithEndpoints) trySend(ctx context.Context, req *GetRequest) (*http.Response, context.CancelFunc, error) {
	return retryWithEndpoints(ctx, p.retry, p.endpoints, req.timeout, func(ctx context.Context, endpoint string) (*http.Response, error) {
		return p.send(ctx, p.clients[endpoint], req)
	})
}

// send sends a request to the specified URL via the client with the given
// headers.
func (p *ProxyWithEndpoints) send(ctx context.Context, client *http.Client, req *GetRequest) (*http.Response, error) {
	headers, err := makeRequestHeaders(req)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidArgument, err)
	}
	httpReq.Header = headers

	resp, err := client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrRequestTimeout, err)
		}

		return nil, fmt.Errorf("%w: %v", ErrInternal, err)
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()

	message, _ := io.ReadAll(resp.Body)
	header := resp.Header.Clone()
	switch errorType := resp.Header.Get("X-Dragonfly-Error-Type"); errorType {
	case "backend":
		return nil, &BackendError{Message: string(message), Header: header, StatusCode: resp.StatusCode}
	case "proxy":
		return nil, &ProxyError{Message: string(message), Header: header, StatusCode: resp.StatusCode}
	case "dfdaemon":
		return nil, &DfdaemonError{Message: string(message)}
	case "":
		return nil, &ProxyError{
			Message:    fmt.Sprintf("unexpected status code from proxy: %d", resp.StatusCode),
			Header:     header,
			StatusCode: resp.StatusCode,
		}
	default:
		return nil, &ProxyError{
			Message:    fmt.Sprintf("unknown error type from proxy: %s", errorType),
			Header:     header,
			StatusCode: resp.StatusCode,
		}
	}
}
