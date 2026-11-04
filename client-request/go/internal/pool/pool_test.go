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
	assert.Equal(0, p.Size())

	addr := "http://proxy1.com"
	_, err := p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())

	_, err = p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())

	addr = "http://proxy2.com"
	_, err = p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(2, p.Size())
}

func TestEntryCleanup(t *testing.T) {
	assert := assert.New(t)
	p := New(testFactory, 10, 10*time.Millisecond)

	addr := "http://proxy1.com"
	_, err := p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())

	time.Sleep(50 * time.Millisecond)

	addr = "http://proxy2.com"
	_, err = p.Entry(addr, addr)
	assert.NoError(err)
	assert.Equal(1, p.Size())
}
