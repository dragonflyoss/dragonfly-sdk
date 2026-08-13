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
	"time"
)

// Factory creates a new HTTP client for the given address.
type Factory func(addr string) (*http.Client, error)

// entry pairs a client with its last active time.
type entry struct {
	client    *http.Client
	activedAt time.Time
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

	mu        sync.Mutex
	clients   map[string]*entry
	cleanupAt time.Time
}

// New creates a new client pool.
func New(factory Factory, capacity int, idleTimeout time.Duration) *Pool {
	return &Pool{
		factory:     factory,
		capacity:    capacity,
		idleTimeout: idleTimeout,
		clients:     make(map[string]*entry),
		cleanupAt:   time.Now(),
	}
}

// Entry gets or creates a client for the given key.
func (p *Pool) Entry(key, addr string) (*http.Client, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Cleanup idle clients first.
	p.cleanupIdleEntries()

	if e, ok := p.clients[key]; ok {
		e.activedAt = time.Now()
		return e.client, nil
	}

	client, err := p.factory(addr)
	if err != nil {
		return nil, err
	}

	p.clients[key] = &entry{client: client, activedAt: time.Now()}
	return client, nil
}

// Size returns the current pool size.
func (p *Pool) Size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.clients)
}

// cleanupIdleEntries removes idle entries that exceed capacity or idle
// timeout. The caller must hold the lock.
func (p *Pool) cleanupIdleEntries() {
	now := time.Now()

	// Avoid hot cleanup.
	if now.Sub(p.cleanupAt) < p.idleTimeout/2 {
		return
	}

	exceedsCapacity := len(p.clients) > p.capacity
	for key, e := range p.clients {
		if !exceedsCapacity && now.Sub(e.activedAt) <= p.idleTimeout {
			continue
		}

		e.client.CloseIdleConnections()
		delete(p.clients, key)
	}

	p.cleanupAt = now
}
