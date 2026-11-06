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

	"github.com/cenkalti/backoff/v5"

	"d7y.io/dragonfly-sdk/client-request/go/internal/pool"
	"d7y.io/dragonfly-sdk/client-request/go/internal/selector"
)

// ProxyWithSeedPeersOption configures the ProxyWithSeedPeers.
type ProxyWithSeedPeersOption func(p *ProxyWithSeedPeers)

// WithProxyWithSeedPeersMaxRetries sets how many times a request is retried
// after a transient failure, each retry on the next seed peer. Default 1, at
// most 10. A transient failure is a timeout, a connection error, or a 5xx, 408
// or 429 answer. Any other answer returns at once.
func WithProxyWithSeedPeersMaxRetries(retries uint8) ProxyWithSeedPeersOption {
	return func(p *ProxyWithSeedPeers) { p.proxy.retry.maxRetries = retries }
}

// WithProxyWithSeedPeersBackoff sets the exponential backoff between retries,
// none retrying at once. Every request starts from a fresh copy of b.
func WithProxyWithSeedPeersBackoff(b *backoff.ExponentialBackOff) ProxyWithSeedPeersOption {
	return func(p *ProxyWithSeedPeers) { p.proxy.retry.backoff = b }
}

// ProxyWithSeedPeers is the HTTP proxy client that sends requests via the seed
// peers given at construction, selected by the consistent hash ring without
// connecting to the scheduler.
type ProxyWithSeedPeers struct {
	// proxy is the proxy sending the requests via the seed peers.
	proxy *Proxy
}

// NewWithSeedPeers creates a ProxyWithSeedPeers that sends requests via the
// given seed peers, such as the ones SchedulerClient.ListSeedPeers returns.
// The hosts must not be modified afterwards.
func NewWithSeedPeers(seedPeers []SeedPeer, opts ...ProxyWithSeedPeersOption) (*ProxyWithSeedPeers, error) {
	p := &ProxyWithSeedPeers{
		proxy: &Proxy{
			retry:            defaultRetryPolicy,
			seedPeerSelector: selector.New(),
		},
	}
	p.proxy.clientPool = pool.New(httpClientFactory{seedPeerSelector: p.proxy.seedPeerSelector}.newHTTPClient, defaultClientPoolCapacity, defaultClientPoolIdleTimeout)
	for _, opt := range opts {
		opt(p)
	}

	if err := p.validateSeedPeers(seedPeers); err != nil {
		return nil, err
	}

	if p.proxy.retry.maxRetries > 10 {
		return nil, fmt.Errorf("%w: max retries must be between 0 and 10", ErrInvalidArgument)
	}

	p.proxy.seedPeerSelector.Update(seedPeers)
	return p, nil
}

// UpdateSeedPeers replaces the seed peers, such as with the ones listed again.
// The hash ring is rebuilt only when the seed peers change, so a task stays on
// its replicas. Invalid seed peers, such as none, are ignored, and the hosts
// must not be modified afterwards.
func (p *ProxyWithSeedPeers) UpdateSeedPeers(seedPeers []SeedPeer) {
	if err := p.validateSeedPeers(seedPeers); err != nil {
		return
	}

	p.proxy.seedPeerSelector.Update(seedPeers)
}

// validateSeedPeers validates the seed peers, which the hash ring keys by host
// name.
func (p *ProxyWithSeedPeers) validateSeedPeers(seedPeers []SeedPeer) error {
	if len(seedPeers) == 0 {
		return fmt.Errorf("%w: seed peers must not be empty", ErrInvalidArgument)
	}

	names := make(map[string]struct{}, len(seedPeers))
	for _, seedPeer := range seedPeers {
		if seedPeer.Host == nil {
			return fmt.Errorf("%w: seed peer host must not be nil", ErrInvalidArgument)
		}

		if seedPeer.Host.Name == "" {
			return fmt.Errorf("%w: seed peer name must not be empty", ErrInvalidArgument)
		}

		if _, ok := names[seedPeer.Host.Name]; ok {
			return fmt.Errorf("%w: seed peer name %q is duplicated", ErrInvalidArgument, seedPeer.Host.Name)
		}
		names[seedPeer.Host.Name] = struct{}{}
	}

	return nil
}

// Get sends a GET request via the seed peers and returns a response with a
// streaming body the caller must close, see Request.Get.
func (p *ProxyWithSeedPeers) Get(ctx context.Context, req *GetRequest) (*GetResponse, error) {
	return p.proxy.Get(ctx, req)
}

// GetInto sends a GET request via the seed peers and writes the response
// body into w, see Request.GetInto.
func (p *ProxyWithSeedPeers) GetInto(ctx context.Context, req *GetRequest, w io.Writer) (*GetResponse, error) {
	return p.proxy.GetInto(ctx, req, w)
}

// LookupEndpoints returns the proxy endpoints of the seed peers that would
// serve the request, see Request.LookupEndpoints.
func (p *ProxyWithSeedPeers) LookupEndpoints(ctx context.Context, req *GetRequest) ([]string, error) {
	return p.proxy.LookupEndpoints(ctx, req)
}
