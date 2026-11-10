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
	"io"
	"net/http"
	"slices"

	"github.com/cenkalti/backoff/v5"
)

// ProxyWithEndpointsOption configures the ProxyWithEndpoints.
type ProxyWithEndpointsOption func(p *ProxyWithEndpoints)

// WithProxyWithEndpointsMaxRetries sets how many times a request is retried
// after a transient failure, each retry on the next endpoint. Default 1, at
// most 3. A transient failure is a timeout, a connection error, or a 5xx, 408
// or 429 answer. Any other answer returns at once.
func WithProxyWithEndpointsMaxRetries(retries uint8) ProxyWithEndpointsOption {
	return func(p *ProxyWithEndpoints) { p.retry.maxRetries = retries }
}

// WithProxyWithEndpointsBackoff sets the exponential backoff between retries,
// none retrying at once. Every request starts from a fresh copy of b.
func WithProxyWithEndpointsBackoff(b *backoff.ExponentialBackOff) ProxyWithEndpointsOption {
	return func(p *ProxyWithEndpoints) { p.retry.backoff = b }
}

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

		client, err := httpClientFactory{}.newHTTPClient(endpoint)
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

	attempts, err := p.retry.newAttempts(p.endpoints, req.timeout)
	if err != nil {
		return nil, err
	}

	resp, cancel, err := attempts.run(ctx, func(ctx context.Context, endpoint string) (*http.Response, error) {
		return p.sendTo(ctx, endpoint, req)
	})
	if err != nil {
		return nil, err
	}

	// Send the request again on the next endpoint for the bytes left when the
	// body breaks off.
	return &GetResponse{
		Success:    true,
		Header:     resp.Header.Clone(),
		StatusCode: resp.StatusCode,
		Body:       newResumableBody(ctx, p, req, attempts, resp, cancel),
	}, nil
}

// GetInto sends a GET request to a remote server via the seed peer endpoints
// of the Dragonfly and writes the response body directly into the provided
// writer. The request is sent to a randomly picked endpoint and a transient
// failure, a body that breaks off included, is retried on the others up to
// the max retries, the body sent again for the bytes left when the response
// tells its length, so the writer is never rewound.
func (p *ProxyWithEndpoints) GetInto(ctx context.Context, req *GetRequest, w io.Writer) (*GetResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	attempts, err := p.retry.newAttempts(p.endpoints, req.timeout)
	if err != nil {
		return nil, err
	}

	resp, cancel, err := attempts.run(ctx, func(ctx context.Context, endpoint string) (*http.Response, error) {
		return p.sendTo(ctx, endpoint, req)
	})
	if err != nil {
		return nil, err
	}

	// Send the request again on the next endpoint for the bytes left when the
	// body breaks off.
	body := newResumableBody(ctx, p, req, attempts, resp, cancel)
	defer body.Close()

	// Drain the response body into the writer.
	if err := body.copyTo(w); err != nil {
		return nil, err
	}

	return &GetResponse{
		Success:    true,
		Header:     resp.Header.Clone(),
		StatusCode: resp.StatusCode,
	}, nil
}

// sendTo sends the request once to the seed peer proxy at endpoint through its
// client.
func (p *ProxyWithEndpoints) sendTo(ctx context.Context, endpoint string, req *GetRequest) (*http.Response, error) {
	client, ok := p.clients[endpoint]
	if !ok {
		return nil, fmt.Errorf("%w: no client for endpoint %s", ErrInternal, endpoint)
	}

	return req.send(ctx, client)
}
