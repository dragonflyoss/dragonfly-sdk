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

package hashring

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// The fixed vectors below are shared with the Rust test suite
// (client-request/rust/tests/consistency.rs). Both sides must produce the
// same outputs; do not change one without the other.

func TestSipHashVectors(t *testing.T) {
	assert := assert.New(t)
	assert.Equal(uint64(0x1e5a582b8d945969), hashVNode(VNode{id: 0, name: "seed-peer-1"}))
	assert.Equal(uint64(0xb5db98265419376c), hashVNode(VNode{id: 1, name: "seed-peer-1"}))
	assert.Equal(uint64(0xf36f748c486b09ef), hashVNode(VNode{id: 0, name: "seed-peer-2"}))
	assert.Equal(uint64(0xf0eda07426c5cac1), hashVNode(VNode{id: 511, name: "seed-peer-3"}))

	assert.Equal(uint64(0xaa86f08bc4878a68), hashKey("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"))
	assert.Equal(uint64(0xa78dd821b4023714), hashKey("test-task"))
	assert.Equal(uint64(0x20dd4eb33d9590f2), hashKey(""))
}

func TestGetWithReplicasVectors(t *testing.T) {
	ring := New(3)
	for _, name := range []string{"seed-peer-1", "seed-peer-2", "seed-peer-3"} {
		ring.Add(name)
	}

	sel := func(key string, replicas int) []string {
		var names []string
		for _, vnode := range ring.GetWithReplicas(key, replicas) {
			names = append(names, vnode.String())
		}
		return names
	}

	assert := assert.New(t)
	assert.Equal([]string{"seed-peer-3|2", "seed-peer-1|2", "seed-peer-3|0"}, sel("task-a", 2))
	assert.Equal([]string{"seed-peer-2|0", "seed-peer-2|1", "seed-peer-1|0"}, sel("task-b", 2))
	assert.Equal([]string{"seed-peer-2|1", "seed-peer-1|0", "seed-peer-3|2"}, sel("task-c", 2))
	assert.Equal([]string{
		"seed-peer-3|2",
		"seed-peer-1|2",
		"seed-peer-3|0",
		"seed-peer-1|1",
		"seed-peer-2|2",
		"seed-peer-3|1",
		"seed-peer-2|0",
		"seed-peer-2|1",
		"seed-peer-1|0",
	}, sel("task-a", 100))
}

func TestVNodeString(t *testing.T) {
	assert := assert.New(t)
	vnode := VNode{id: 1, name: "default-pod-1"}
	assert.Equal("default-pod-1|1", vnode.String())
	assert.Equal("default-pod-1", vnode.Name())
}

func TestAddAndLen(t *testing.T) {
	assert := assert.New(t)
	ring := New(2)
	assert.True(ring.IsEmpty())

	ring.Add("default-pod-1")
	assert.Equal(2, ring.Len())
	ring.Add("default-pod-2")
	assert.Equal(4, ring.Len())
	assert.False(ring.IsEmpty())
}

func TestGetEmptyRing(t *testing.T) {
	assert := assert.New(t)
	ring := New(2)
	_, ok := ring.Get("test_key")
	assert.False(ok)
	assert.Nil(ring.GetWithReplicas("test_key", 2))
}

func TestGetWithReplicasSizes(t *testing.T) {
	assert := assert.New(t)
	ring := New(2)
	ring.Add("default-pod-1")
	ring.Add("default-pod-2")

	// Replicas within the ring return replicas+1 vnodes.
	assert.Len(ring.GetWithReplicas("test_key", 2), 3)

	// Replicas equal to the ring length wrap around to replicas+1 vnodes.
	assert.Len(ring.GetWithReplicas("test_key", 4), 5)

	// Replicas larger than the ring shrink to the entire ring.
	assert.Len(ring.GetWithReplicas("test_key", 100), 4)
}

func TestAddOrderDoesNotAffectGet(t *testing.T) {
	assert := assert.New(t)

	ringA := New(150)
	for _, n := range []string{"default-pod-1", "default-pod-2", "default-pod-3"} {
		ringA.Add(n)
	}

	ringB := New(150)
	for _, n := range []string{"default-pod-3", "default-pod-1", "default-pod-2"} {
		ringB.Add(n)
	}

	for _, key := range []string{"task-a", "task-b", "task-c", "task-d", "task-e"} {
		va, _ := ringA.Get(key)
		vb, _ := ringB.Get(key)
		assert.Equal(va.String(), vb.String())
	}
}
