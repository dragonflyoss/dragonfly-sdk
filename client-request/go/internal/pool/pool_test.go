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

package pool

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func testFactory(addr string) (*http.Client, error) {
	return &http.Client{}, nil
}

func TestEntryGetOrCreate(t *testing.T) {
	assert := assert.New(t)
	p := New(testFactory, 10, 600*time.Second)

	// Initially, the pool is empty.
	assert.Equal(0, p.Size())

	// Get a client for the first time. A new client should be created.
	addr := "http://proxy1.com"
	_, err := p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())

	// Get a client for the same proxy again. It should be reused.
	_, err = p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())

	// Get a client for a different proxy. A new client should be created.
	addr = "http://proxy2.com"
	_, err = p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(2, p.Size())
}

func TestEntryCleanup(t *testing.T) {
	assert := assert.New(t)
	p := New(testFactory, 10, 10*time.Millisecond)

	// Create a client.
	addr := "http://proxy1.com"
	_, err := p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())

	// Wait for cleanup above client.
	time.Sleep(50 * time.Millisecond)

	// Create another client. The first client should have been cleaned up.
	addr = "http://proxy2.com"
	_, err = p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())
}
