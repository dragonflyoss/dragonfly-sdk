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
	"syscall"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	"d7y.io/dragonfly/v2/pkg/idgen"
	"d7y.io/dragonfly/v2/pkg/oci"
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

// SeedPeer is a seed peer listed by the scheduler, with the result of its
// health check.
type SeedPeer = selector.SeedPeer

// SchedulerClient is the client of the scheduler for listing the seed peers to
// share through NewWithSeedPeers.
type SchedulerClient struct {
	// scheduler is the client of the scheduler service.
	scheduler *selector.Scheduler
}

// NewSchedulerClient creates a client of the scheduler at the given endpoint,
// e.g. "http://scheduler-service:8002", bounding its requests by
// requestTimeout. The caller must close it.
func NewSchedulerClient(endpoint string, requestTimeout time.Duration) (*SchedulerClient, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid scheduler endpoint: %s", ErrInvalidArgument, endpoint)
	}

	scheduler, err := selector.NewScheduler(u.Host, requestTimeout)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to connect to scheduler %s: %w", ErrInternal, endpoint, err)
	}

	return &SchedulerClient{scheduler: scheduler}, nil
}

// ListSeedPeers lists the seed peers with their health, taking up to the
// request timeout plus 3 seconds of health checks.
func (s *SchedulerClient) ListSeedPeers(ctx context.Context) ([]SeedPeer, error) {
	seedPeers, err := s.scheduler.ListSeedPeers(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to list seed peers: %w", ErrInternal, err)
	}

	return seedPeers, nil
}

// Close closes the connection to the scheduler.
func (s *SchedulerClient) Close() error {
	return s.scheduler.Close()
}

// Proxy is the HTTP proxy client that sends requests via Dragonfly.
type Proxy struct {
	// retry is the retry policy of the requests.
	retry retryPolicy

	// schedulerRequestTimeout is the timeout of requests to the scheduler
	// service.
	schedulerRequestTimeout time.Duration

	// healthCheckInterval is the interval of health check for seed peers.
	healthCheckInterval time.Duration

	// seedPeerSelector is the selector service for selecting seed peers.
	seedPeerSelector *selector.SeedPeerSelector

	// clientPool is the pool of clients.
	clientPool *pool.Pool

	// registryClient is the http client of the registry requests.
	registryClient *http.Client

	// scheduler is the scheduler service the seed peers are listed from, nil
	// in a ProxyWithSeedPeers.
	scheduler *selector.Scheduler
}

// New creates a Proxy that connects to the given scheduler endpoint,
// e.g. "http://scheduler-service:8002".
func New(ctx context.Context, schedulerEndpoint string, opts ...ProxyOption) (*Proxy, error) {
	p := &Proxy{
		retry:                   defaultRetryPolicy,
		schedulerRequestTimeout: defaultSchedulerRequestTimeout,
		healthCheckInterval:     defaultHealthCheckInterval,
		seedPeerSelector:        selector.New(),
		registryClient:          oci.DefaultHTTPClient(),
	}
	p.clientPool = pool.New(httpClientFactory{seedPeerSelector: p.seedPeerSelector}.newHTTPClient, defaultClientPoolCapacity, defaultClientPoolIdleTimeout)
	for _, opt := range opts {
		opt(p)
	}

	if err := p.validate(); err != nil {
		return nil, err
	}

	schedulerClient, err := NewSchedulerClient(schedulerEndpoint, p.schedulerRequestTimeout)
	if err != nil {
		return nil, err
	}

	if err := p.seedPeerSelector.Refresh(ctx, schedulerClient.scheduler); err != nil {
		schedulerClient.Close()
		return nil, fmt.Errorf("%w: failed to create seed peer selector: %v", ErrInternal, err)
	}

	// Run the selector service in the background to refresh the seed peers
	// periodically.
	go p.seedPeerSelector.Run(schedulerClient.scheduler, p.healthCheckInterval)

	p.scheduler = schedulerClient.scheduler
	return p, nil
}

// Close stops the background seed peer refresh and closes the scheduler
// connection.
func (p *Proxy) Close() error {
	p.seedPeerSelector.Close()
	p.registryClient.CloseIdleConnections()
	if p.scheduler == nil {
		return nil
	}

	return p.scheduler.Close()
}

// validate validates the input parameters.
func (p *Proxy) validate() error {
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

// httpClientFactory creates the HTTP clients to the seed peer proxies.
type httpClientFactory struct {
	// seedPeerSelector records the answers of the proxies, nil for the clients
	// whose proxies are never ejected.
	seedPeerSelector *selector.SeedPeerSelector
}

// newHTTPClient creates a new HTTP client configured to use the specified
// proxy address, ejecting the proxy that keeps failing to connect when the
// seed peer selector is set. The transport follows the shape of net/http
// DefaultTransport with a larger idle connection pool, since each client
// serves a single seed peer proxy host.
//
// TODO(chlins): Support client certificates and set the insecure skip verify
// based on the certificates.
func (f httpClientFactory) newHTTPClient(proxyAddr string) (*http.Client, error) {
	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to set proxy %s: %v", ErrInternal, proxyAddr, err)
	}

	var transport http.RoundTripper = &http.Transport{
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
	}
	if f.seedPeerSelector != nil {
		transport = &ejectionTransport{
			transport:        transport,
			seedPeerSelector: f.seedPeerSelector,
			endpoint:         proxyAddr,
		}
	}

	return &http.Client{Transport: transport}, nil
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

	attempts, err := p.retry.newAttempts(endpoints, req.timeout)
	if err != nil {
		return nil, err
	}

	resp, cancel, err := attempts.run(ctx, func(ctx context.Context, endpoint string) (*http.Response, error) {
		return p.sendTo(ctx, endpoint, req)
	})
	if err != nil {
		return nil, err
	}

	// Send the request again on the next seed peer for the bytes left when
	// the body breaks off.
	return &GetResponse{
		Success:    true,
		Header:     resp.Header.Clone(),
		StatusCode: resp.StatusCode,
		Body:       newResumableBody(ctx, p, req, attempts, resp, cancel),
	}, nil
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

	attempts, err := p.retry.newAttempts(endpoints, req.timeout)
	if err != nil {
		return nil, err
	}

	resp, cancel, err := attempts.run(ctx, func(ctx context.Context, endpoint string) (*http.Response, error) {
		return p.sendTo(ctx, endpoint, req)
	})
	if err != nil {
		return nil, err
	}

	// Send the request again on the next seed peer for the bytes left when
	// the body breaks off.
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

// LookupEndpoints looks up the proxy endpoints (e.g., "http://127.0.0.1:4001")
// of the seed peers serving the request, in the consistent hash ring selection
// order for the request's task id. It returns up to the replicas of the
// request, skipping the unhealthy or ejected ones.
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
		return nil, fmt.Errorf("%w: failed to select seed peers: %v", ErrInternal, err)
	}

	// Skip the ejected proxies, failing when all are ejected.
	endpoints := make([]string, 0, len(seedPeers))
	for _, peer := range seedPeers {
		endpoint := p.seedPeerSelector.ProxyEndpoint(peer)
		if !p.seedPeerSelector.IsEjected(endpoint) {
			endpoints = append(endpoints, endpoint)
		}
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("%w: seed peers of task %s are all ejected", ErrInternal, taskID)
	}

	return endpoints, nil
}

// selectSeedPeers selects the seed peers the scope addresses for the task: the
// replicas in the consistent hash ring order, or all seed peers.
func (p *Proxy) selectSeedPeers(id string, scope Scope, replicas int) ([]*commonv2.Host, error) {
	if scope == ScopeAllSeedPeers {
		return p.seedPeerSelector.SelectAll()
	}

	return p.seedPeerSelector.Select(id, uint32(replicas))
}

// sendTo sends the request once to the seed peer proxy at endpoint through its
// pooled client.
func (p *Proxy) sendTo(ctx context.Context, endpoint string, req *GetRequest) (*http.Response, error) {
	entry, err := p.clientPool.Entry(endpoint, endpoint)
	if err != nil {
		return nil, err
	}

	guard := entry.RequestGuard()
	defer guard.Done()

	return req.send(ctx, entry.Client)
}

// ejectionTransport records whether a seed peer proxy answered, to eject the
// proxy that keeps failing to connect.
type ejectionTransport struct {
	// transport sends the requests.
	transport http.RoundTripper

	// seedPeerSelector is the seed peer selector recording the answers.
	seedPeerSelector *selector.SeedPeerSelector

	// endpoint is the endpoint of the seed peer proxy.
	endpoint string
}

// RoundTrip sends the request, recording any response as a success and a
// refused or unreachable proxy as a failure.
func (t *ejectionTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.transport.RoundTrip(req)
	switch {
	case err == nil:
		t.seedPeerSelector.OnSuccess(t.endpoint)
	case errors.Is(err, syscall.ECONNREFUSED), errors.Is(err, syscall.EHOSTUNREACH), errors.Is(err, syscall.ENETUNREACH):
		t.seedPeerSelector.OnFailure(t.endpoint)
	}

	return resp, err
}

// CloseIdleConnections closes the idle connections of the transport.
func (t *ejectionTransport) CloseIdleConnections() {
	if transport, ok := t.transport.(interface{ CloseIdleConnections() }); ok {
		transport.CloseIdleConnections()
	}
}
