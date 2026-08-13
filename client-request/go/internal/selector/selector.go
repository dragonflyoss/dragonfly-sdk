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

// Package selector selects seed peers from the scheduler service with a
// consistent hash ring, ported from the Rust client's selector
// (dragonfly-client-request).
package selector

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	commonv2 "d7y.io/api/v2/pkg/apis/common/v2"
	schedulerv2 "d7y.io/api/v2/pkg/apis/scheduler/v2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"

	"d7y.io/dragonfly-sdk/client-request/go/internal/hashring"
)

// healthCheckTimeout is the health check timeout for seed peers.
const healthCheckTimeout = 5 * time.Second

// vnodesPerHost is the default number of virtual nodes per host.
const vnodesPerHost = 512

// seedPeerType is the host type of seed peers, refer to
// dragonfly-client-config/src/dfdaemon.rs#HostType.
const seedPeerType = 1

// Selector selects items from a list of items by a specific criteria.
type Selector interface {
	// Select selects hosts based on the given taskID and number of replicas.
	Select(taskID string, replicas uint32) ([]*commonv2.Host, error)
}

// SeedPeerSelector selects seed peers from the scheduler service.
type SeedPeerSelector struct {
	// healthCheckInterval is the interval of health check for seed peers.
	healthCheckInterval time.Duration

	// requestTimeout is the timeout of requests to the scheduler service.
	requestTimeout time.Duration

	// schedulerClient is the client to communicate with the scheduler service.
	schedulerClient schedulerv2.SchedulerClient

	mu    sync.RWMutex
	hosts map[string]*commonv2.Host
	ring  *hashring.VNodeHashRing

	// refreshMu protects hot refresh.
	refreshMu sync.Mutex

	done      chan struct{}
	closeOnce sync.Once
}

// New creates a new seed peer selector and refreshes the seed peers once.
func New(ctx context.Context, schedulerClient schedulerv2.SchedulerClient, healthCheckInterval, requestTimeout time.Duration) (*SeedPeerSelector, error) {
	s := &SeedPeerSelector{
		healthCheckInterval: healthCheckInterval,
		requestTimeout:      requestTimeout,
		schedulerClient:     schedulerClient,
		hosts:               make(map[string]*commonv2.Host),
		ring:                hashring.New(vnodesPerHost),
		done:                make(chan struct{}),
	}

	if err := s.refresh(ctx); err != nil {
		return nil, err
	}

	return s, nil
}

// Run refreshes the seed peers periodically until Close is called.
func (s *SeedPeerSelector) Run() {
	ticker := time.NewTicker(s.healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.refresh(context.Background()); err != nil {
				continue
			}
		case <-s.done:
			return
		}
	}
}

// Close stops the periodic refresh.
func (s *SeedPeerSelector) Close() {
	s.closeOnce.Do(func() { close(s.done) })
}

// Select selects seed peers for the given taskID with the number of replicas.
func (s *SeedPeerSelector) Select(taskID string, replicas uint32) ([]*commonv2.Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.hosts) == 0 {
		return nil, fmt.Errorf("host not found: seed peers")
	}

	// The number of replicas cannot exceed the total number of seed peers.
	expectedReplicas := int(replicas)
	if expectedReplicas > s.ring.Len() {
		expectedReplicas = s.ring.Len()
	}

	var seedPeers []*commonv2.Host
	for _, vnode := range s.ring.GetWithReplicas(taskID, expectedReplicas) {
		if host, ok := s.hosts[vnode.Name()]; ok {
			seedPeers = append(seedPeers, host)
		}
	}

	if len(seedPeers) == 0 {
		return nil, fmt.Errorf("host not found: selected seed peers")
	}

	return seedPeers, nil
}

// refresh updates the seed peers data.
func (s *SeedPeerSelector) refresh(ctx context.Context) error {
	// Only one refresh can be running at a time.
	if !s.refreshMu.TryLock() {
		return nil
	}
	defer s.refreshMu.Unlock()

	seedPeers, err := s.listSeedPeers(ctx)
	if err != nil {
		return err
	}

	// Process health checks concurrently. The number of seed peers in a
	// cluster is usually small, so check all of them simultaneously.
	var wg sync.WaitGroup
	healthy := make([]*commonv2.Host, len(seedPeers))
	for i, peer := range seedPeers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			addr := net.JoinHostPort(peer.Ip, strconv.Itoa(int(peer.Port)))
			if err := checkHealth(ctx, addr); err == nil {
				healthy[i] = peer
			}
		}()
	}
	wg.Wait()

	hosts := make(map[string]*commonv2.Host, len(seedPeers))
	ring := hashring.New(vnodesPerHost)
	for _, peer := range healthy {
		if peer == nil {
			continue
		}

		ring.Add(peer.Name)
		hosts[peer.Name] = peer
	}

	// The write lock is held for a very short time.
	s.mu.Lock()
	s.hosts = hosts
	s.ring = ring
	s.mu.Unlock()

	return nil
}

// listSeedPeers lists the seed peers from scheduler.
func (s *SeedPeerSelector) listSeedPeers(ctx context.Context) ([]*commonv2.Host, error) {
	ctx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	hostType := uint32(seedPeerType)
	resp, err := s.schedulerClient.ListHosts(ctx, &schedulerv2.ListHostsRequest{Type: &hostType})
	if err != nil {
		return nil, err
	}

	return resp.Hosts, nil
}

// checkHealth checks the health of a seed peer.
func checkHealth(ctx context.Context, addr string) error {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer conn.Close()

	ctx, cancel := context.WithTimeout(ctx, healthCheckTimeout)
	defer cancel()

	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}
