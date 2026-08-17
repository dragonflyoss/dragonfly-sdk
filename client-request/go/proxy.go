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
	"strconv"
	"strings"
	"time"

	schedulerv2 "d7y.io/api/v2/pkg/apis/scheduler/v2"
	"d7y.io/dragonfly/v2/pkg/idgen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"d7y.io/dragonfly-sdk/client-request/go/internal/pool"
	"d7y.io/dragonfly-sdk/client-request/go/internal/selector"
)

const (
	// poolMaxIdlePerHost is the max idle connections per host.
	poolMaxIdlePerHost = 1024

	// keepAliveInterval is the keep alive interval for TCP connection.
	keepAliveInterval = 60 * time.Second

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

// WithProxyMaxRetries sets the maximum number of retries.
func WithProxyMaxRetries(retries uint8) ProxyOption {
	return func(p *Proxy) { p.maxRetries = retries }
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

// Proxy is the HTTP proxy client that sends requests via Dragonfly.
type Proxy struct {
	// maxRetries is the number of times to retry a request.
	maxRetries uint8

	// schedulerRequestTimeout is the timeout of requests to the scheduler
	// service.
	schedulerRequestTimeout time.Duration

	// healthCheckInterval is the interval of health check for seed peers.
	healthCheckInterval time.Duration

	// seedPeerSelector is the selector service for selecting seed peers.
	seedPeerSelector *selector.SeedPeerSelector

	// clientPool is the pool of clients.
	clientPool *pool.Pool

	// schedulerConn is the connection to the scheduler service.
	schedulerConn *grpc.ClientConn
}

// New creates a Proxy that connects to the given scheduler endpoint,
// e.g. "http://scheduler-service:8002".
func New(ctx context.Context, schedulerEndpoint string, opts ...ProxyOption) (*Proxy, error) {
	p := &Proxy{
		maxRetries:              defaultMaxRetries,
		schedulerRequestTimeout: defaultSchedulerRequestTimeout,
		healthCheckInterval:     defaultHealthCheckInterval,
		clientPool:              pool.New(httpClientFactory, defaultClientPoolCapacity, defaultClientPoolIdleTimeout),
	}
	for _, opt := range opts {
		opt(p)
	}

	target, err := p.validate(schedulerEndpoint)
	if err != nil {
		return nil, err
	}

	schedulerConn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("%w: failed to connect to scheduler %s: %v", ErrInternal, schedulerEndpoint, err)
	}

	seedPeerSelector, err := selector.New(ctx, schedulerv2.NewSchedulerClient(schedulerConn), p.healthCheckInterval, p.schedulerRequestTimeout)
	if err != nil {
		schedulerConn.Close()
		return nil, fmt.Errorf("%w: failed to create seed peer selector: %v", ErrInternal, err)
	}

	// Run the selector service in the background to refresh the seed peers
	// periodically.
	go seedPeerSelector.Run()

	p.seedPeerSelector = seedPeerSelector
	p.schedulerConn = schedulerConn
	return p, nil
}

// Close stops the background seed peer refresh and closes the scheduler
// connection.
func (p *Proxy) Close() error {
	p.seedPeerSelector.Close()
	return p.schedulerConn.Close()
}

// validate validates the input parameters and returns the scheduler grpc target.
func (p *Proxy) validate(schedulerEndpoint string) (string, error) {
	u, err := url.Parse(schedulerEndpoint)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("%w: invalid scheduler endpoint: %s", ErrInvalidArgument, schedulerEndpoint)
	}

	if p.schedulerRequestTimeout < 100*time.Millisecond {
		return "", fmt.Errorf("%w: scheduler request timeout must be at least 100 milliseconds", ErrInvalidArgument)
	}

	if p.healthCheckInterval < time.Second || p.healthCheckInterval > 600*time.Second {
		return "", fmt.Errorf("%w: health check interval must be between 1 and 600 seconds", ErrInvalidArgument)
	}

	if p.maxRetries > 10 {
		return "", fmt.Errorf("%w: max retries must be between 0 and 10", ErrInvalidArgument)
	}

	return u.Host, nil
}

// httpClientFactory creates a new HTTP client configured to use the specified
// proxy address.
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
				KeepAlive: keepAliveInterval,
			}).DialContext,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true},
			MaxIdleConnsPerHost: poolMaxIdlePerHost,
		},
	}, nil
}

// Get sends a GET request to a remote server via the Dragonfly and returns a
// response with a streaming body. The caller must close the body.
func (p *Proxy) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	// The timeout covers the whole request including the body read, so the
	// cancel function is called when the body is closed.
	ctx, cancel := context.WithTimeout(ctx, req.timeout)
	resp, err := p.trySend(ctx, req)
	if err != nil {
		cancel()
		return nil, err
	}

	return &GetResponse{
		Success:    true,
		Header:     resp.Header.Clone(),
		StatusCode: resp.StatusCode,
		Body:       &cancelReadCloser{body: resp.Body, cancel: cancel},
	}, nil
}

// GetInto sends a GET request to a remote server via the Dragonfly and writes
// the response body directly into the provided writer.
func (p *Proxy) GetInto(ctx context.Context, req *GetRequest, w io.Writer) (*GetResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, req.timeout)
	defer cancel()

	resp, err := p.trySend(ctx, req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("%w: %v", ErrRequestTimeout, err)
		}

		return nil, err
	}
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

// LookupEndpoints looks up the endpoints (e.g., "http://127.0.0.1:4000") of
// the seed peers serving the request, in the consistent hash ring selection
// order for the request's task id. The number of endpoints is limited by the
// max retries of the Proxy, and the same seed peer may appear multiple times
// when it is selected for retries.
func (p *Proxy) LookupEndpoints(ctx context.Context, req *GetRequest) ([]string, error) {
	taskID, err := generateTaskID(req)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
	}

	seedPeers, err := p.seedPeerSelector.Select(taskID, uint32(p.maxRetries))
	if err != nil {
		return nil, fmt.Errorf("%w: failed to select seed peers from scheduler: %v", ErrInternal, err)
	}

	addrs := make([]string, 0, len(seedPeers))
	for _, peer := range seedPeers {
		addrs = append(addrs, fmt.Sprintf("http://%s", net.JoinHostPort(peer.Ip, strconv.Itoa(int(peer.Port)))))
	}

	return addrs, nil
}

// generateTaskID generates the task id for the request parameters, identical
// to the Rust client's task id generation.
func generateTaskID(req *GetRequest) (string, error) {
	if req.contentForCalculatingTaskID != "" {
		return idgen.TaskIDV2ByContent(req.contentForCalculatingTaskID), nil
	}

	if req.enableTaskIDBasedBlobDigest && idgen.IsBlobURL(req.url) {
		return idgen.TaskIDV2ByBlobDigest(req.url)
	}

	return idgen.TaskIDV2ByURLBased(req.url, req.pieceLength, req.tag, req.application, req.filteredQueryParams, ""), nil
}

// clients returns pooled HTTP clients with proxy configuration for the
// selected seed peers.
func (p *Proxy) clients(req *GetRequest) ([]*http.Client, error) {
	tasakID, err := generateTaskID(req)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to generate task id: %v", ErrInternal, err)
	}

	seedPeers, err := p.seedPeerSelector.Select(tasakID, uint32(p.maxRetries))
	if err != nil {
		return nil, fmt.Errorf("%w: failed to select seed peers from scheduler: %v", ErrInternal, err)
	}

	clients := make([]*http.Client, 0, len(seedPeers))
	for _, peer := range seedPeers {
		// TODO(chlins): Support client https scheme.
		addr := fmt.Sprintf("http://%s", net.JoinHostPort(peer.Ip, strconv.Itoa(int(peer.ProxyPort))))
		client, err := p.clientPool.Entry(addr, addr)
		if err != nil {
			return nil, err
		}

		clients = append(clients, client)
	}

	return clients, nil
}

// trySend processes requests with retries and returns the first successful
// response.
func (p *Proxy) trySend(ctx context.Context, req *GetRequest) (*http.Response, error) {
	clients, err := p.clients(req)
	if err != nil {
		return nil, err
	}

	if len(clients) == 0 {
		return nil, fmt.Errorf("%w: no available client entries to send request", ErrInternal)
	}

	var lastErr error
	for _, client := range clients {
		resp, err := p.send(ctx, client, req)
		if err == nil {
			return resp, nil
		}

		lastErr = err
	}

	return nil, lastErr
}

// send sends a request to the specified URL via the client with the given
// headers.
func (p *Proxy) send(ctx context.Context, client *http.Client, req *GetRequest) (*http.Response, error) {
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

// makeRequestHeaders applies p2p related headers to the request headers.
func makeRequestHeaders(req *GetRequest) (http.Header, error) {
	headers := req.header.Clone()
	if headers == nil {
		headers = make(http.Header)
	}

	if req.pieceLength != nil {
		headers.Set("X-Dragonfly-Piece-Length", strconv.FormatUint(*req.pieceLength, 10))
	}

	if req.tag != "" {
		headers.Set("X-Dragonfly-Tag", req.tag)
	}

	if req.application != "" {
		headers.Set("X-Dragonfly-Application", req.application)
	}

	if req.contentForCalculatingTaskID != "" {
		headers.Set("X-Dragonfly-Content-For-Calculating-Task-ID", req.contentForCalculatingTaskID)
	}

	headers.Set("X-Dragonfly-Enable-Task-ID-Based-Blob-Digest", strconv.FormatBool(req.enableTaskIDBasedBlobDigest))

	if req.priority != nil {
		headers.Set("X-Dragonfly-Priority", strconv.FormatInt(int64(*req.priority), 10))
	}

	if len(req.filteredQueryParams) > 0 {
		headers.Set("X-Dragonfly-Filtered-Query-Params", strings.Join(req.filteredQueryParams, ","))
	}

	headers.Set("X-Dragonfly-Use-P2P", "true")
	return headers, nil
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
