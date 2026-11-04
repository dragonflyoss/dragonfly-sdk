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

// Package pool provides a client pool for managing reusable HTTP client
// instances with automatic cleanup, ported from the Rust client's pool
// (dragonfly-client-util).
package pool

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// Factory creates a new HTTP client for the given address.
type Factory func(addr string) (*http.Client, error)

// RequestGuard automatically tracks active requests for a client.
type RequestGuard struct {
	// activeRequests is the number of the active requests of the entry.
	activeRequests *atomic.Int64
}

// Done decrements the active request count.
func (g *RequestGuard) Done() {
	g.activeRequests.Add(-1)
}

// Entry is the wrapper for clients in the pool.
type Entry struct {
	// Client is the HTTP client instance.
	Client *http.Client

	// activeRequests is the number of the active requests.
	activeRequests atomic.Int64

	// activedAt is the last time the client was used, in nanoseconds since
	// the Unix epoch.
	activedAt atomic.Int64
}

// newEntry creates a new entry with the given client.
func newEntry(client *http.Client) *Entry {
	e := &Entry{Client: client}
	e.setActivedAt(time.Now())
	return e
}

// RequestGuard creates a request guard to track active requests. The caller
// must call Done when the request finishes.
func (e *Entry) RequestGuard() *RequestGuard {
	e.activeRequests.Add(1)
	return &RequestGuard{activeRequests: &e.activeRequests}
}

// setActivedAt updates the last active time.
func (e *Entry) setActivedAt(activedAt time.Time) {
	e.activedAt.Store(activedAt.UnixNano())
}

// hasActiveRequests checks if the client has active requests.
func (e *Entry) hasActiveRequests() bool {
	return e.activeRequests.Load() > 0
}

// idleDuration returns the idle duration since last active.
func (e *Entry) idleDuration(now time.Time) time.Duration {
	return time.Duration(now.UnixNano() - e.activedAt.Load())
}

// Pool is a client pool that provides connection reuse, automatic cleanup and
// capacity control.
type Pool struct {
	// factory creates clients for addresses.
	factory Factory

	// capacity is the maximum number of clients in the pool.
	capacity int

	// idleTimeout is the idle timeout for clients in the pool.
	idleTimeout time.Duration

	// mu protects access to the clients map.
	mu sync.Mutex

	// clients maps keys to client entries.
	clients map[string]*Entry

	// cleanupAt is the last time the pool performed cleanup.
	cleanupAt time.Time
}

// New creates a new client pool.
func New(factory Factory, capacity int, idleTimeout time.Duration) *Pool {
	return &Pool{
		factory:     factory,
		capacity:    capacity,
		idleTimeout: idleTimeout,
		clients:     make(map[string]*Entry),
		cleanupAt:   time.Now(),
	}
}

// Entry gets or creates a client entry for the given key.
func (p *Pool) Entry(key, addr string) (*Entry, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Cleanup idle clients first.
	p.cleanupIdleEntries()

	// Try to get existing client.
	if e, ok := p.clients[key]; ok {
		e.setActivedAt(time.Now())
		return e, nil
	}

	// Create new client.
	client, err := p.factory(addr)
	if err != nil {
		return nil, err
	}

	e := newEntry(client)
	p.clients[key] = e
	return e, nil
}

// RemoveEntry removes a client entry if it has no active requests.
func (p *Pool) RemoveEntry(key string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if e, ok := p.clients[key]; ok && !e.hasActiveRequests() {
		delete(p.clients, key)
	}
}

// Size returns the current pool size.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

// Clear removes all clients from the pool.
func (p *Pool) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	clear(p.clients)
}

// cleanupIdleEntries removes idle entries that exceed capacity or idle
// timeout, retaining the entries with active requests. The caller must hold
// the lock.
func (p *Pool) cleanupIdleEntries() {
	now := time.Now()

	// Avoid hot cleanup.
	if now.Sub(p.cleanupAt) < p.idleTimeout/2 {
		return
	}

	exceedsCapacity := len(p.clients) > p.capacity
	for key, e := range p.clients {
		isRecent := e.idleDuration(now) <= p.idleTimeout
		if e.hasActiveRequests() || (!exceedsCapacity && isRecent) {
			continue
		}

		e.Client.CloseIdleConnections()
		delete(p.clients, key)
	}

	p.cleanupAt = now
}
