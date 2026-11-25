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

# Mirrors d7y.io/hashring@v0.3.0 hashring_test.go and consistency_test.go
# TestSiphash, and the dragonfly-hashring crate's src/lib.rs unit tests.

import pytest

from dragonfly_client_request.hashring import (
    DEFAULT_VNODES_PER_NODE,
    VNode,
    VNodeHashRing,
    _hash_key,
)

MOCK_NODES = ["default-seed-client-1", "default-seed-client-2", "default-seed-client-3"]


# Raw SipHash-2-4 values from d7y.io/hashring consistency_test.go TestSiphash
# (lines 35-104) and tests/consistency.rs siphash_matches_go. These pin the
# byte encoding (8-byte little-endian id, name bytes, 0xff terminator) rather
# than only the resulting ring placement.
@pytest.mark.parametrize(
    ("vnode_id", "name", "expected"),
    [
        (0, "seed-peer-1", 0x1E5A582B8D945969),
        (1, "seed-peer-1", 0xB5DB98265419376C),
        (0, "seed-peer-2", 0xF36F748C486B09EF),
        (511, "seed-peer-3", 0xF0EDA07426C5CAC1),
    ],
)
def test_vnode_hash_matches_go_and_rust(vnode_id: int, name: str, expected: int) -> None:
    assert VNode(id=vnode_id, name=name).hash() == expected


@pytest.mark.parametrize(
    ("key", "expected"),
    [
        ("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e", 0xAA86F08BC4878A68),
        ("test-task", 0xA78DD821B4023714),
        ("", 0x20DD4EB33D9590F2),
    ],
)
def test_hash_key_matches_go_and_rust(key: str, expected: int) -> None:
    assert _hash_key(key) == expected


def test_vnode_formats_as_name_and_id() -> None:
    vnode = VNode(id=1, name="default-seed-client-1")
    assert str(vnode) == "default-seed-client-1|1"
    assert vnode.name == "default-seed-client-1"


@pytest.mark.parametrize(
    ("vnodes_per_node", "nodes", "expected_len"),
    [
        (3, [], 0),
        (2, ["default-seed-client-1"], 2),
        (2, ["default-seed-client-1", "default-seed-client-2"], 4),
    ],
)
def test_add_inserts_vnodes_per_node_for_each_node(
    vnodes_per_node: int, nodes: list[str], expected_len: int
) -> None:
    ring = VNodeHashRing(vnodes_per_node=vnodes_per_node)
    for name in nodes:
        ring.add(name)

    assert len(ring) == expected_len
    assert ring.is_empty() == (expected_len == 0)
    assert ring._nodes == sorted(ring._nodes, key=lambda node: node[0])


def test_new_uses_default_vnodes_per_node() -> None:
    ring = VNodeHashRing()
    ring.add("default-seed-client-1")
    assert len(ring) == DEFAULT_VNODES_PER_NODE == 512


def test_get_returns_none_for_empty_ring() -> None:
    ring = VNodeHashRing(vnodes_per_node=2)
    assert ring.get("task") is None
    assert ring.get_with_replicas("task", 2) == []


def test_get_maps_every_key_to_the_single_node() -> None:
    ring = VNodeHashRing(vnodes_per_node=2)
    ring.add("default-seed-client-1")
    for key in ["", "task-a", "task-b"]:
        vnode = ring.get(key)
        assert vnode is not None
        assert vnode.name == "default-seed-client-1"


def test_same_key_maps_to_the_same_vnode() -> None:
    ring = VNodeHashRing(vnodes_per_node=2)
    for name in MOCK_NODES:
        ring.add(name)

    assert ring.get("task") == ring.get("task")


@pytest.mark.parametrize(("replicas", "expected_len"), [(2, 3), (4, 5), (100, 4)])
def test_get_with_replicas_clamps_to_the_ring(replicas: int, expected_len: int) -> None:
    ring = VNodeHashRing(vnodes_per_node=2)
    ring.add("default-seed-client-1")
    ring.add("default-seed-client-2")
    assert len(ring.get_with_replicas("task", replicas)) == expected_len


def test_add_order_does_not_affect_get() -> None:
    ring_a = VNodeHashRing()
    for name in MOCK_NODES:
        ring_a.add(name)

    ring_b = VNodeHashRing()
    for name in reversed(MOCK_NODES):
        ring_b.add(name)

    for key in ["task-a", "task-b", "task-c", "task-d", "task-e"]:
        assert ring_a.get(key) == ring_b.get(key)
