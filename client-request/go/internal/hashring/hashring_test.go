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

	assert.Len(ring.GetWithReplicas("test_key", 2), 3)
	assert.Len(ring.GetWithReplicas("test_key", 4), 5)
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
