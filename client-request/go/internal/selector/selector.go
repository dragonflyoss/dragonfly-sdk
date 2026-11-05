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

// Package selector selects seed peers with a consistent hash ring, ported
// from the Rust client's selector (dragonfly-client-request).
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
	"d7y.io/hashring"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// healthCheckTimeout is the health check timeout for seed peers.
const healthCheckTimeout = 5 * time.Second

// seedPeerType is the host type of seed peers, refer to
// dragonfly-client-config/src/dfdaemon.rs#HostType.
const seedPeerType = 1

// maxFailedCount is the maximum count of consecutive failed requests to a seed
// peer proxy, reaching which ejects it.
const maxFailedCount = 5

// ejectionCooldown is how long an ejected seed peer proxy is skipped after its
// ejection.
const ejectionCooldown = 30 * time.Second

// Selector selects items from a list of items by a specific criteria.
type Selector interface {
	// Select selects hosts based on the given taskID and number of replicas.
	Select(taskID string, replicas uint32) ([]*commonv2.Host, error)

	// SelectAll selects all hosts regardless of the taskID.
	SelectAll() ([]*commonv2.Host, error)
}

// SeedPeer is a seed peer listed by the scheduler, with the result of its
// health check.
type SeedPeer struct {
	// Host is the host of the seed peer.
	Host *commonv2.Host

	// Healthy reports whether the seed peer passed its health check.
	Healthy bool
}

// Scheduler lists the seed peers from the scheduler service.
type Scheduler struct {
	// conn is the connection to the scheduler service.
	conn *grpc.ClientConn

	// client is the client to communicate with the scheduler service.
	client schedulerv2.SchedulerClient

	// requestTimeout is the timeout of requests to the scheduler service.
	requestTimeout time.Duration
}

// NewScheduler creates a scheduler at the given grpc target, e.g.
// "scheduler-service:8002", its requests bounded by requestTimeout.
func NewScheduler(target string, requestTimeout time.Duration) (*Scheduler, error) {
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, err
	}

	return &Scheduler{
		conn:           conn,
		client:         schedulerv2.NewSchedulerClient(conn),
		requestTimeout: requestTimeout,
	}, nil
}

// Client returns the client to communicate with the scheduler service.
func (s *Scheduler) Client() schedulerv2.SchedulerClient {
	return s.client
}

// Close closes the connection to the scheduler service.
func (s *Scheduler) Close() error {
	return s.conn.Close()
}

// ListSeedPeers lists the seed peers from the scheduler and checks the health
// of each concurrently.
func (s *Scheduler) ListSeedPeers(ctx context.Context) ([]SeedPeer, error) {
	listCtx, cancel := context.WithTimeout(ctx, s.requestTimeout)
	defer cancel()

	hostType := uint32(seedPeerType)
	resp, err := s.client.ListHosts(listCtx, &schedulerv2.ListHostsRequest{Type: &hostType})
	if err != nil {
		return nil, err
	}

	// The number of seed peers in a cluster is usually small, so check all of
	// them simultaneously.
	var wg sync.WaitGroup
	seedPeers := make([]SeedPeer, len(resp.Hosts))
	for i, host := range resp.Hosts {
		wg.Go(func() {
			addr := net.JoinHostPort(host.Ip, strconv.Itoa(int(host.Port)))
			seedPeers[i] = SeedPeer{Host: host, Healthy: s.checkHealth(ctx, addr) == nil}
		})
	}
	wg.Wait()

	return seedPeers, nil
}

// checkHealth checks the health of a seed peer.
func (s *Scheduler) checkHealth(ctx context.Context, addr string) error {
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

// ejection holds the consecutive failed requests to a seed peer proxy, and the
// ejection they led to.
type ejection struct {
	// failedCount is the count of consecutive failed requests since the last
	// successful one.
	failedCount int

	// ejectedAt is the time of the most recent ejection, zero if the proxy has
	// never been ejected.
	ejectedAt time.Time
}

// SeedPeerSelector selects seed peers with a consistent hash ring.
//
// The hashring is built from every seed peer, healthy or not, and a task is
// served only by its replicas, the seed peers following its position on the
// ring, which hold it. A replica is skipped when it fails its health check, or
// for ejectionCooldown once its proxy fails maxFailedCount requests in a row,
// and the task fails when every replica is skipped, rather than moving to a
// seed peer that does not hold it.
//
//	task ──▶ ring position ──▶ replicas ─┬─ seed-peer-1 ──unhealthy or ejected──▶ skipped
//	                                     └─ seed-peer-2 ──available────────────▶ selected
//	                           every replica skipped ──▶ host not found
type SeedPeerSelector struct {
	// mu protects hosts and ring.
	mu sync.RWMutex

	// hosts is a map of seed peers by their host name, healthy or not.
	hosts map[string]SeedPeer

	// ring is a consistent hash ring of seed peers.
	ring *hashring.VNodeHashRing

	// ejectionsMu protects ejections.
	ejectionsMu sync.RWMutex

	// ejections is the ejections of the seed peer proxies by proxy endpoint.
	ejections map[string]*ejection

	// refreshMu protects hot refresh.
	refreshMu sync.Mutex

	// done is closed when the selector is closed.
	done chan struct{}

	// closeOnce ensures that the selector is closed only once.
	closeOnce sync.Once
}

// New creates a new seed peer selector without any seed peer.
func New() *SeedPeerSelector {
	return &SeedPeerSelector{
		hosts:     make(map[string]SeedPeer),
		ring:      hashring.New(),
		ejections: make(map[string]*ejection),
		done:      make(chan struct{}),
	}
}

// Run refreshes the seed peers from the scheduler every healthCheckInterval
// until Close is called.
func (s *SeedPeerSelector) Run(scheduler *Scheduler, healthCheckInterval time.Duration) {
	ticker := time.NewTicker(healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := s.Refresh(context.Background(), scheduler); err != nil {
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

// Refresh refreshes the seed peers from the scheduler.
func (s *SeedPeerSelector) Refresh(ctx context.Context, scheduler *Scheduler) error {
	// Only one refresh can be running at a time.
	if !s.refreshMu.TryLock() {
		return nil
	}
	defer s.refreshMu.Unlock()

	seedPeers, err := scheduler.ListSeedPeers(ctx)
	if err != nil {
		return err
	}

	s.Update(seedPeers)
	return nil
}

// Update replaces the seed peers and their health.
func (s *SeedPeerSelector) Update(seedPeers []SeedPeer) {
	hosts := make(map[string]SeedPeer, len(seedPeers))
	for _, seedPeer := range seedPeers {
		hosts[seedPeer.Host.Name] = seedPeer
	}

	// Rebuilding the ring costs DefaultVNodesPerNode hashes per host and a full sort,
	// so reuse the current ring when the host names are unchanged and only
	// refresh the host records and their health.
	s.mu.RLock()
	unchanged := len(hosts) == len(s.hosts)
	for name := range hosts {
		if _, ok := s.hosts[name]; !ok {
			unchanged = false
			break
		}
	}
	s.mu.RUnlock()

	if unchanged {
		s.mu.Lock()
		s.hosts = hosts
		s.mu.Unlock()
		return
	}

	ring := hashring.New()
	for name := range hosts {
		ring.Add(name)
	}

	s.mu.Lock()
	s.hosts = hosts
	s.ring = ring
	s.mu.Unlock()
}

// OnFailure handles a request to the proxy at endpoint that failed before the
// proxy answered, ejecting the proxy after maxFailedCount of them in a row.
func (s *SeedPeerSelector) OnFailure(endpoint string) {
	s.ejectionsMu.Lock()
	defer s.ejectionsMu.Unlock()

	e, ok := s.ejections[endpoint]
	if !ok {
		e = &ejection{}
		s.ejections[endpoint] = e
	}

	e.failedCount++
	if e.failedCount >= maxFailedCount {
		e.failedCount = 0
		e.ejectedAt = time.Now()
	}
}

// OnSuccess handles a request answered by the proxy at endpoint, clearing its
// failures and ejection.
func (s *SeedPeerSelector) OnSuccess(endpoint string) {
	// Most requests succeed with nothing to clear, so check under the read
	// lock before taking the write lock to delete.
	s.ejectionsMu.RLock()
	_, ok := s.ejections[endpoint]
	s.ejectionsMu.RUnlock()
	if !ok {
		return
	}

	s.ejectionsMu.Lock()
	defer s.ejectionsMu.Unlock()

	delete(s.ejections, endpoint)
}

// isEjected reports whether the proxy of the seed peer is still within the
// cooldown of its most recent ejection.
func (s *SeedPeerSelector) isEjected(host *commonv2.Host) bool {
	s.ejectionsMu.RLock()
	defer s.ejectionsMu.RUnlock()

	// Most of the time no proxy is ejected, so skip formatting the endpoint.
	if len(s.ejections) == 0 {
		return false
	}

	e, ok := s.ejections[s.ProxyEndpoint(host)]
	return ok && !e.ejectedAt.IsZero() && time.Since(e.ejectedAt) < ejectionCooldown
}

// Select selects the available ones of the replicas, the first replicas
// distinct seed peers for the given taskID in the consistent hash ring order.
// The number of replicas is clamped to the number of seed peers.
func (s *SeedPeerSelector) Select(taskID string, replicas uint32) ([]*commonv2.Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.hosts) == 0 {
		return nil, fmt.Errorf("host not found: seed peers")
	}

	// The number of replicas cannot exceed the total number of seed peers.
	expectedReplicas := min(int(replicas), len(s.hosts))

	// Walk the ring clockwise from the task position collecting distinct seed
	// peers, growing the window until enough distinct seed peers are found or
	// the whole ring is covered.
	var replicaSeedPeers []SeedPeer
	for window := expectedReplicas * 2; ; window *= 2 {
		replicaSeedPeers = replicaSeedPeers[:0]
		seen := make(map[string]struct{}, expectedReplicas)
		vnodes := s.ring.GetWithReplicas(taskID, window)
		for _, vnode := range vnodes {
			if _, ok := seen[vnode.Name()]; ok {
				continue
			}
			seen[vnode.Name()] = struct{}{}

			seedPeer, ok := s.hosts[vnode.Name()]
			if !ok {
				continue
			}

			replicaSeedPeers = append(replicaSeedPeers, seedPeer)
			if len(replicaSeedPeers) == expectedReplicas {
				break
			}
		}

		if len(replicaSeedPeers) == expectedReplicas || len(vnodes) >= s.ring.Len() {
			break
		}
	}

	// Serve the task only from the replicas that passed their health check and
	// are not ejected, failing when none is.
	var seedPeers []*commonv2.Host
	for _, seedPeer := range replicaSeedPeers {
		if seedPeer.Healthy && !s.isEjected(seedPeer.Host) {
			seedPeers = append(seedPeers, seedPeer.Host)
		}
	}

	if len(seedPeers) == 0 {
		return nil, fmt.Errorf("host not found: available seed peers")
	}

	return seedPeers, nil
}

// SelectAll selects all seed peers that passed their health check, regardless
// of the taskID.
func (s *SeedPeerSelector) SelectAll() ([]*commonv2.Host, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var seedPeers []*commonv2.Host
	for _, seedPeer := range s.hosts {
		if seedPeer.Healthy {
			seedPeers = append(seedPeers, seedPeer.Host)
		}
	}

	if len(seedPeers) == 0 {
		return nil, fmt.Errorf("host not found: seed peers")
	}

	return seedPeers, nil
}

// ProxyEndpoint returns the endpoint of the proxy of a seed peer, e.g.
// "http://127.0.0.1:4001".
func (s *SeedPeerSelector) ProxyEndpoint(host *commonv2.Host) string {
	// TODO(chlins): Support client https scheme.
	return fmt.Sprintf("http://%s", net.JoinHostPort(host.Ip, strconv.Itoa(int(host.ProxyPort))))
}
