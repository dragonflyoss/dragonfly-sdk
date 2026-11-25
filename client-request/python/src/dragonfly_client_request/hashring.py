#     Copyright 2026 The Dragonfly Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Consistent hash ring with virtual nodes. It places nodes and keys the same
way as the d7y.io/hashring module and the dragonfly-hashring crate, so the
scheduler, the client and the SDKs select the same seed peer for a task.
"""

from __future__ import annotations

import struct
from bisect import bisect_left
from dataclasses import dataclass

__all__ = ["DEFAULT_VNODES_PER_NODE", "VNode", "VNodeHashRing"]

# DEFAULT_VNODES_PER_NODE is the default number of virtual nodes of each node on
# the ring, shared by the seed peer selectors of dragonfly and dragonfly-sdk.
DEFAULT_VNODES_PER_NODE = 512

_MASK = 0xFFFFFFFFFFFFFFFF


def _rotl(x: int, b: int) -> int:
    return ((x << b) | (x >> (64 - b))) & _MASK


def _siphash24(data: bytes) -> int:
    """Computes SipHash-2-4 of data with zero keys, the hash the ring is pinned
    to.
    """
    v0 = 0x736F6D6570736575
    v1 = 0x646F72616E646F6D
    v2 = 0x6C7967656E657261
    v3 = 0x7465646279746573

    def round_() -> None:
        nonlocal v0, v1, v2, v3
        v0 = (v0 + v1) & _MASK
        v1 = _rotl(v1, 13) ^ v0
        v0 = _rotl(v0, 32)
        v2 = (v2 + v3) & _MASK
        v3 = _rotl(v3, 16) ^ v2
        v0 = (v0 + v3) & _MASK
        v3 = _rotl(v3, 21) ^ v0
        v2 = (v2 + v1) & _MASK
        v1 = _rotl(v1, 17) ^ v2
        v2 = _rotl(v2, 32)

    blocks = len(data) // 8
    for i in range(blocks):
        (m,) = struct.unpack_from("<Q", data, i * 8)
        v3 ^= m
        round_()
        round_()
        v0 ^= m

    b = (len(data) & 0xFF) << 56
    for i, byte in enumerate(data[blocks * 8 :]):
        b |= byte << (8 * i)

    v3 ^= b
    round_()
    round_()
    v0 ^= b

    v2 ^= 0xFF
    round_()
    round_()
    round_()
    round_()

    return (v0 ^ v1 ^ v2 ^ v3) & _MASK


def _hash_key(key: str) -> int:
    """Returns the position of the key on the ring, hashed with the Rust std
    Hash encoding of a string.
    """
    return _siphash24(key.encode() + b"\xff")


@dataclass(frozen=True)
class VNode:
    """A virtual node on the consistent hash ring. Each physical node is
    represented by multiple virtual nodes to better balance key distribution.
    """

    # id is the replica index of this virtual node for its physical node.
    id: int

    # name is the physical node name this virtual node represents.
    name: str

    def __str__(self) -> str:
        return f"{self.name}|{self.id}"

    def hash(self) -> int:
        """Returns the position of the virtual node on the ring.

        The encoding is the Rust std Hash of the first implementation of the
        virtual node: the replica id as 8 little-endian bytes, the name bytes
        and the 0xff terminator of a string, hashed with SipHash-2-4 and zero
        keys. Changing it moves every key to another node, so it is a wire
        compatibility contract shared with the dragonfly-hashring crate.
        """
        return _siphash24(struct.pack("<Q", self.id) + self.name.encode() + b"\xff")


class VNodeHashRing:
    """A consistent hash ring that uses virtual nodes to improve key
    distribution. When a physical node is added, vnodes_per_node virtual nodes
    are inserted into the ring. It is not safe for concurrent use.
    """

    def __init__(self, vnodes_per_node: int = DEFAULT_VNODES_PER_NODE) -> None:
        self._vnodes_per_node = vnodes_per_node

        # The ring sorted by the position of the virtual node.
        self._nodes: list[tuple[int, VNode]] = []

    def add(self, name: str) -> None:
        """Adds vnodes_per_node virtual nodes of the named node to the ring."""
        for vnode_id in range(self._vnodes_per_node):
            vnode = VNode(id=vnode_id, name=name)
            self._nodes.append((vnode.hash(), vnode))

        self._nodes.sort(key=lambda node: node[0])

    def get(self, key: str) -> VNode | None:
        """Returns the virtual node responsible for the key, and None when the
        ring is empty.
        """
        if not self._nodes:
            return None

        return self._nodes[self._search(_hash_key(key))][1]

    def get_with_replicas(self, key: str, replicas: int) -> list[VNode]:
        """Returns the virtual node responsible for the key along with the next
        replicas virtual nodes clockwise on the ring, and an empty list when the
        ring is empty. When replicas is larger than the length of the ring, the
        result shrinks to the entire ring.
        """
        if not self._nodes:
            return []

        take = len(self._nodes) if replicas > len(self._nodes) else replicas + 1
        start = self._search(_hash_key(key))
        return [self._nodes[(start + i) % len(self._nodes)][1] for i in range(take)]

    def __len__(self) -> int:
        return len(self._nodes)

    def is_empty(self) -> bool:
        """Reports whether the ring has no virtual nodes."""
        return not self._nodes

    def _search(self, key: int) -> int:
        """Returns the index of the first node whose key is not less than the
        key, wrapping to the first node past the end of the ring.
        """
        return bisect_left(self._nodes, key, key=lambda node: node[0]) % len(self._nodes)
