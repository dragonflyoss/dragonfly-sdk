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

// Package hashring provides a consistent hash ring with virtual nodes that is
// bit-for-bit compatible with the Rust client's VNodeHashRing
// (dragonfly-client-util), which wraps the hashring crate with SipHash-2-4 and
// zero keys. Keys and vnodes are hashed with the Rust std Hash encoding: a
// usize is written as 8 native-endian bytes (little-endian on all supported
// targets) and a string as its bytes followed by a 0xff terminator.
package hashring

import (
	"encoding/binary"
	"fmt"
	"sort"

	"github.com/dchest/siphash"
)

// VNode is a virtual node on the consistent hash ring. Each physical node is
// represented by multiple vnodes to better balance key distribution.
type VNode struct {
	// id is the replica index of this vnode for its physical node.
	id int

	// name is the physical node name this vnode represents.
	name string
}

// Name returns the physical node name associated with this vnode.
func (v VNode) Name() string {
	return v.name
}

// String formats the virtual node as "name|id".
func (v VNode) String() string {
	return fmt.Sprintf("%s|%d", v.name, v.id)
}

// node pairs a vnode with its position on the ring.
type node struct {
	key   uint64
	vnode VNode
}

// VNodeHashRing is a consistent hash ring that uses virtual nodes to improve
// key distribution.
type VNodeHashRing struct {
	// replicaCount is the number of vnodes to create per physical node.
	replicaCount int

	// nodes is the ring sorted by key.
	nodes []node
}

// New creates a new vnode-based hash ring.
func New(replicaCount int) *VNodeHashRing {
	return &VNodeHashRing{replicaCount: replicaCount}
}

// Add adds replicaCount virtual nodes to the ring for the given node name.
func (r *VNodeHashRing) Add(name string) {
	for id := 0; id < r.replicaCount; id++ {
		vnode := VNode{id: id, name: name}
		r.nodes = append(r.nodes, node{key: hashVNode(vnode), vnode: vnode})
	}

	sort.Slice(r.nodes, func(i, j int) bool { return r.nodes[i].key < r.nodes[j].key })
}

// Get returns the vnode responsible for key, or false when the ring is empty.
func (r *VNodeHashRing) Get(key string) (VNode, bool) {
	if len(r.nodes) == 0 {
		return VNode{}, false
	}

	return r.nodes[r.search(hashKey(key))].vnode, true
}

// GetWithReplicas returns the vnode responsible for key along with the next
// replicas vnodes after it, walking the ring clockwise. Returns nil when the
// ring is empty. If replicas is larger than the length of the ring, the result
// shrinks to just contain the entire ring.
func (r *VNodeHashRing) GetWithReplicas(key string, replicas int) []VNode {
	if len(r.nodes) == 0 {
		return nil
	}

	take := replicas + 1
	if replicas > len(r.nodes) {
		take = len(r.nodes)
	}

	start := r.search(hashKey(key))
	vnodes := make([]VNode, 0, take)
	for i := 0; i < take; i++ {
		vnodes = append(vnodes, r.nodes[(start+i)%len(r.nodes)].vnode)
	}

	return vnodes
}

// Len returns the number of vnodes in the hash ring.
func (r *VNodeHashRing) Len() int {
	return len(r.nodes)
}

// IsEmpty returns true if the ring has no elements.
func (r *VNodeHashRing) IsEmpty() bool {
	return len(r.nodes) == 0
}

// search returns the ring index of the first node with key >= k, wrapping to 0
// past the end.
func (r *VNodeHashRing) search(k uint64) int {
	n := sort.Search(len(r.nodes), func(i int) bool { return r.nodes[i].key >= k })
	return n % len(r.nodes)
}

// hashVNode hashes a vnode with the Rust derive(Hash) field order: the replica
// id then the name.
func hashVNode(v VNode) uint64 {
	buf := make([]byte, 0, 8+len(v.name)+1)
	buf = binary.LittleEndian.AppendUint64(buf, uint64(v.id))
	buf = append(buf, v.name...)
	buf = append(buf, 0xff)
	return siphash.Hash(0, 0, buf)
}

// hashKey hashes a string key with the Rust String Hash encoding.
func hashKey(key string) uint64 {
	buf := make([]byte, 0, len(key)+1)
	buf = append(buf, key...)
	buf = append(buf, 0xff)
	return siphash.Hash(0, 0, buf)
}
