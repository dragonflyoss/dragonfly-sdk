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
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"d7y.io/dragonfly/v2/pkg/idgen"
	"github.com/cenkalti/backoff/v5"

	"d7y.io/dragonfly-sdk/client-request/go/internal/pool"
	"d7y.io/dragonfly-sdk/client-request/go/internal/selector"
)

const (
	// poolMaxIdlePerHost is the max idle connections per host.
	poolMaxIdlePerHost = 1024

	// keepAliveInterval is the keep alive interval for TCP connection.
	keepAliveInterval = 60 * time.Second

	// idleConnTimeout is the maximum amount of time an idle connection remains
	// idle before closing itself.
	idleConnTimeout = 90 * time.Second

	// dialTimeout is the maximum amount of time a dial waits for a connect to
	// complete.
	dialTimeout = 30 * time.Second

	// tlsHandshakeTimeout is the maximum amount of time to wait for a TLS
	// handshake.
	tlsHandshakeTimeout = 10 * time.Second

	// expectContinueTimeout is the maximum amount of time to wait for a
	// server's first response headers after fully writing the request headers
	// when the request has an "Expect: 100-continue" header.
	expectContinueTimeout = 1 * time.Second

	// defaultClientPoolIdleTimeout is the default idle timeout(30 minutes) for
	// clients in the pool.
	defaultClientPoolIdleTimeout = 30 * time.Minute

	// defaultClientPoolCapacity is the default capacity of the client pool.
	defaultClientPoolCapacity = 128

	// defaultSchedulerRequestTimeout is the default timeout(5 seconds) for
	// requests to the scheduler service.
	defaultSchedulerRequestTimeout = 5 * time.Second

	// defaultHealthCheckInterval is the default interval of health check for
	// seed peers.
	defaultHealthCheckInterval = 60 * time.Second

	// defaultMaxRetries is the default number of times to retry a request.
	defaultMaxRetries = 1
)

// ProxyOption configures the Proxy.
type ProxyOption func(p *Proxy)

// WithProxyMaxRetries sets how many times a request is retried after a
// transient failure, each retry on the next seed peer. Default 1, at most 10.
// A transient failure is a timeout, a connection error, or a 5xx, 408 or 429
// answer. Any other answer returns at once.
func WithProxyMaxRetries(retries uint8) ProxyOption {
	return func(p *Proxy) { p.retry.maxRetries = retries }
}

// WithProxyBackoff sets the exponential backoff between retries, none retrying
// at once. Every request starts from a fresh copy of b.
func WithProxyBackoff(b *backoff.ExponentialBackOff) ProxyOption {
	return func(p *Proxy) { p.retry.backoff = b }
}

// WithProxySchedulerRequestTimeout sets the timeout of requests to the
// scheduler service.
func WithProxySchedulerRequestTimeout(timeout time.Duration) ProxyOption {
	return func(p *Proxy) { p.schedulerRequestTimeout = timeout }
}

// WithProxyHealthCheckInterval sets the interval of health check for seed
// peers.
func WithProxyHealthCheckInterval(interval time.Duration) ProxyOption {
	return func(p *Proxy) { p.healthCheckInterval = interval }
}

// WithProxySeedPeers sets the seed peers of the cluster to select from, such
// as the ones Scheduler.ListSeedPeers returns, instead of the scheduler
// endpoint, which New then ignores: the proxy neither connects to the scheduler
// nor refreshes them, which UpdateSeedPeers replaces, but still selects the
// replicas of every request by the consistent hash ring. Unlike the endpoints
// of NewWithEndpoints, which are the replicas already selected for one
// request, such as by LookupEndpoints.
func WithProxySeedPeers(seedPeers []SeedPeer) ProxyOption {
	return func(p *Proxy) {
		if seedPeers == nil {
			seedPeers = []SeedPeer{}
		}

		p.seedPeers = seedPeers
	}
}

// SeedPeer is a seed peer listed by the scheduler, with the result of its
// health check.
type SeedPeer = selector.SeedPeer

// Scheduler is the scheduler the seed peers are listed from. The seed peers
// its ListSeedPeers lists can be shared, such as by several processes on a
// host, and served through WithProxySeedPeers without each process connecting
// to the scheduler.
type Scheduler = selector.Scheduler

// NewScheduler creates a scheduler at the given endpoint, e.g.
// "http://scheduler-service:8002", its requests bounded by requestTimeout.
// The caller must close it.
func NewScheduler(endpoint string, requestTimeout time.Duration) (*Scheduler, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid scheduler endpoint: %s", ErrInvalidArgument, endpoint)
	}

	scheduler, err := selector.NewScheduler(u.Host, requestTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to connect to scheduler %s: %v", ErrInternal, endpoint, err)
	}

	return scheduler, nil
}

// Ensure Proxy implements the Request interface at compile time.
var _ Request = (*Proxy)(nil)

// Proxy is the HTTP proxy client that sends requests via Dragonfly.
type Proxy struct {
	// retry is the retry policy of the requests.
	retry retryPolicy

	// schedulerRequestTimeout is the timeout of requests to the scheduler
	// service.
	schedulerRequestTimeout time.Duration

	// healthCheckInterval is the interval of health check for seed peers.
	healthCheckInterval time.Duration

	// seedPeers is the seed peers serving the requests instead of the ones
	// the scheduler lists.
	seedPeers []SeedPeer

	// seedPeerSelector is the selector service for selecting seed peers.
	seedPeerSelector *selector.SeedPeerSelector

	// clientPool is the pool of clients.
	clientPool *pool.Pool

	// scheduler is the scheduler service the seed peers are listed from.
	scheduler *selector.Scheduler
}

// New creates a Proxy that connects to the given scheduler endpoint,
// e.g. "http://scheduler-service:8002".
func New(ctx context.Context, schedulerEndpoint string, opts ...ProxyOption) (*Proxy, error) {
	p := &Proxy{
		retry:                   defaultRetryPolicy,
		schedulerRequestTimeout: defaultSchedulerRequestTimeout,
		healthCheckInterval:     defaultHealthCheckInterval,
		clientPool:              pool.New(httpClientFactory, defaultClientPoolCapacity, defaultClientPoolIdleTimeout),
	}
	for _, opt := range opts {
		opt(p)
	}

	if err := p.validate(); err != nil {
		return nil, err
	}

	// Create seed peer selector, from the given seed peers or the ones the
	// scheduler lists.
	p.seedPeerSelector = selector.New()
	if p.seedPeers != nil {
		p.seedPeerSelector.Update(p.seedPeers)
		return p, nil
	}

	scheduler, err := NewScheduler(schedulerEndpoint, p.schedulerRequestTimeout)
	if err != nil {
		return nil, err
	}

	if err := p.seedPeerSelector.Refresh(ctx, scheduler); err != nil {
		scheduler.Close()
		return nil, fmt.Errorf("%w: failed to create seed peer selector: %v", ErrInternal, err)
	}

	// Run the selector service in the background to refresh the seed peers
	// periodically.
	go p.seedPeerSelector.Run(scheduler, p.healthCheckInterval)

	p.scheduler = scheduler
	return p, nil
}

// Close stops the background seed peer refresh and closes the scheduler
// connection.
func (p *Proxy) Close() error {
	p.seedPeerSelector.Close()
	if p.scheduler == nil {
		return nil
	}

	return p.scheduler.Close()
}

// UpdateSeedPeers replaces the seed peers serving the requests, such as the
// ones Scheduler.ListSeedPeers returns, for a proxy created with
// WithProxySeedPeers. The consistent hash ring is rebuilt only when the seed
// peers change, not when their health does, so a task stays on its replicas:
// an unhealthy replica is skipped, the task failing when every replica is, and
// serves it again once it recovers.
func (p *Proxy) UpdateSeedPeers(seedPeers []SeedPeer) {
	p.seedPeerSelector.Update(seedPeers)
}

// validate validates the input parameters.
func (p *Proxy) validate() error {
	if p.seedPeers != nil && len(p.seedPeers) == 0 {
		return fmt.Errorf("%w: seed peers must not be empty", ErrInvalidArgument)
	}

	if p.schedulerRequestTimeout < 100*time.Millisecond {
		return fmt.Errorf("%w: scheduler request timeout must be at least 100 milliseconds", ErrInvalidArgument)
	}

	if p.healthCheckInterval < time.Second || p.healthCheckInterval > 600*time.Second {
		return fmt.Errorf("%w: health check interval must be between 1 and 600 seconds", ErrInvalidArgument)
	}

	if p.retry.maxRetries > 10 {
		return fmt.Errorf("%w: max retries must be between 0 and 10", ErrInvalidArgument)
	}

	return nil
}

// httpClientFactory creates a new HTTP client configured to use the specified
// proxy address. The transport follows the shape of net/http DefaultTransport
// with a larger idle connection pool, since each client serves a single seed
// peer proxy host.
//
// TODO(chlins): Support client certificates and set the insecure skip verify
// based on the certificates.
func httpClientFactory(proxyAddr string) (*http.Client, error) {
	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to set proxy %s: %v", ErrInternal, proxyAddr, err)
	}

	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: keepAliveInterval,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			TLSClientConfig:       &tls.Config{InsecureSkipVerify: true},
			MaxIdleConns:          poolMaxIdlePerHost,
			MaxIdleConnsPerHost:   poolMaxIdlePerHost,
			IdleConnTimeout:       idleConnTimeout,
			TLSHandshakeTimeout:   tlsHandshakeTimeout,
			ExpectContinueTimeout: expectContinueTimeout,
		},
	}, nil
}

// Get sends a GET request to a remote server via the Dragonfly and returns a
// response with a streaming body. The caller must close the body.
func (p *Proxy) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	endpoints, err := p.lookupProxyEndpoints(req)
	if err != nil {
		return nil, err
	}

	resp, cancel, err := p.retry.runWithEndpoints(ctx, endpoints, req.timeout, func(ctx context.Context, endpoint string) (*http.Response, error) {
		return p.sendTo(ctx, endpoint, req)
	})
	if err != nil {
		return nil, err
	}

	return streamResponse(resp, cancel), nil
}

// GetInto sends a GET request to a remote server via the Dragonfly and writes
// the response body directly into the provided writer.
func (p *Proxy) GetInto(ctx context.Context, req *GetRequest, w io.Writer) (*GetResponse, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	endpoints, err := p.lookupProxyEndpoints(req)
	if err != nil {
		return nil, err
	}

	resp, cancel, err := p.retry.runWithEndpoints(ctx, endpoints, req.timeout, func(ctx context.Context, endpoint string) (*http.Response, error) {
		return p.sendTo(ctx, endpoint, req)
	})
	if err != nil {
		return nil, err
	}
	defer cancel()

	return copyResponse(resp, w)
}

// streamResponse builds the response with a streaming body that releases the
// attempt timeout when the body is closed.
func streamResponse(resp *http.Response, cancel context.CancelFunc) *GetResponse {
	return &GetResponse{
		Success:    true,
		Header:     resp.Header.Clone(),
		StatusCode: resp.StatusCode,
		Body:       &cancelReadCloser{body: resp.Body, cancel: cancel},
	}
}

// copyResponse drains the response body into the writer and builds the
// response.
func copyResponse(resp *http.Response, w io.Writer) (*GetResponse, error) {
	defer resp.Body.Close()

	if _, err := io.Copy(w, resp.Body); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrRequestTimeout, err)
		}

		return nil, fmt.Errorf("%w: failed to read response body: %v", ErrInternal, err)
	}

	return &GetResponse{
		Success:    true,
		Header:     resp.Header.Clone(),
		StatusCode: resp.StatusCode,
	}, nil
}

// LookupEndpoints looks up the proxy endpoints (e.g., "http://127.0.0.1:4001")
// of the seed peers serving the request, in the consistent hash ring selection
// order for the request's task id. It returns up to the replicas of the
// request distinct endpoints, clamped to the number of available seed peers.
func (p *Proxy) LookupEndpoints(ctx context.Context, req *GetRequest) ([]string, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}

	return p.lookupProxyEndpoints(req)
}

// lookupProxyEndpoints looks up the proxy endpoints of the seed peers serving the
// request, in the consistent hash ring selection order.
func (p *Proxy) lookupProxyEndpoints(req *GetRequest) ([]string, error) {
	taskID, err := idgen.TaskIDV2(req.url, req.pieceLength, req.tag, req.application, req.filteredQueryParams, req.contentForCalculatingTaskID, req.enableTaskIDBasedBlobDigest)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
	}

	seedPeers, err := p.seedPeerSelector.Select(taskID, uint32(req.replicas))
	if err != nil {
		return nil, fmt.Errorf("%w: failed to select seed peers from scheduler: %v", ErrInternal, err)
	}

	endpoints := make([]string, 0, len(seedPeers))
	for _, peer := range seedPeers {
		endpoints = append(endpoints, p.seedPeerSelector.ProxyEndpoint(peer))
	}

	return endpoints, nil
}

// sendTo sends the request once to the seed peer proxy at endpoint through its
// pooled client, recording whether the proxy answered, so a proxy that keeps
// failing is ejected.
func (p *Proxy) sendTo(ctx context.Context, endpoint string, req *GetRequest) (*http.Response, error) {
	entry, err := p.clientPool.Entry(endpoint, endpoint)
	if err != nil {
		return nil, err
	}

	guard := entry.RequestGuard()
	defer guard.Done()

	// A request the caller cancelled says nothing about the proxy, so only a
	// failure while its context is alive counts.
	resp, err := req.send(ctx, entry.Client)
	switch {
	case err == nil:
		p.seedPeerSelector.OnSuccess(endpoint)
	case errors.Is(err, ErrInternal) && ctx.Err() == nil:
		p.seedPeerSelector.OnFailure(endpoint)
	}

	return resp, err
}

// cancelReadCloser cancels the request timeout context when the body is fully
// read or closed, releasing the timeout resources as soon as the stream ends.
// It follows the same pattern as net/http's cancelTimerBody.
type cancelReadCloser struct {
	body   io.ReadCloser
	cancel context.CancelFunc
}

// Read implements io.Reader. It cancels the timeout context once the read
// reaches EOF or fails; the cancel function is idempotent, so canceling again
// on Close is safe.
func (c *cancelReadCloser) Read(p []byte) (int, error) {
	n, err := c.body.Read(p)
	if err != nil {
		c.cancel()
	}

	return n, err
}

// Close implements io.Closer.
func (c *cancelReadCloser) Close() error {
	err := c.body.Close()
	c.cancel()
	return err
}
