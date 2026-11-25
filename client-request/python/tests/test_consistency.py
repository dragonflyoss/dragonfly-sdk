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

# Cross-language consistency vectors shared with the Go SDK
# (client-request/go/consistency_test.go), the Rust SDK
# (client-request/rust/tests/consistency.rs) and d7y.io/hashring. Every suite
# asserts the same fixed outputs, so a change on any side that moves a task id
# or a seed peer selection fails the build.

import pytest

from dragonfly_client_request.hashring import (
    DEFAULT_VNODES_PER_NODE,
    VNodeHashRing,
)
from dragonfly_client_request.id_generator import task_id

TASK_ID_CASES = [
    (
        "https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar",
        4194304,
        "tag1",
        "app1",
        ["Expires", "Signature"],
        "",
        "2a0c4c713d7f2f65f36b78b79c4b78a6bf5d5f67b76730ed13485d3271482f1c",
    ),
    (
        "https://example.com/file.txt",
        None,
        "",
        "",
        [],
        "",
        "7fcf06e5f0b1e443065c1a563eed788eb2e168a05c6ad9c4b319f7a976322be0",
    ),
    (
        "https://example.com/file.txt?z=9&b=2&a=1",
        None,
        "foo",
        "bar",
        ["z"],
        "",
        "8b3f6e9b9b8fe20903bced565cfd1d0aaef354a4c17573f0c2c1979210443f9d",
    ),
    (
        "https://example.com/file.txt?b=2&a=1&b=1",
        None,
        "",
        "",
        ["c"],
        "",
        "7c8801d0596be5e8f9449d5c4af23866c72fe5205119c0e5912981f3b16a37aa",
    ),
    (
        "https://example.com/file.txt?k=a b&m=x*y&n=c~d",
        1024,
        "",
        "",
        ["none"],
        "",
        "6196a6846023f6d3c1e4d30f6c86f3d4186e4c664a33e5692b0e04e49b26a9af",
    ),
    (
        "https://example.com/file.txt?a=1&b=2",
        None,
        "foo",
        "",
        ["a", "b"],
        "",
        "c8f4b41117329d54af920010394f6f607bac707e933ab2f18d372e3dd4c7fcb3",
    ),
    (
        "https://example.com/file.txt?b=2&a=1",
        None,
        "",
        "",
        [],
        "",
        "980ee327518ccc5a7c30703e1a2232e8ba9047b39431f940636c85b6146f8b9a",
    ),
    (
        "https://example.com/file.txt?a=1;x&b=2",
        None,
        "",
        "",
        ["none"],
        "",
        "370933e9d7ab42b3ed90287213cd5f8def6091c8606da5b2dc79436061b06c34",
    ),
    (
        "http://www.xx.yy/path?u=f&x=y&m=z&x=s#size",
        None,
        "",
        "",
        ["x", "m"],
        "",
        "3570c3c808f06fd250a7a60634b9275d72c56edc248576508bb06264c2c65825",
    ),
    (
        "",
        None,
        "",
        "",
        [],
        "This is a test file",
        "e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249",
    ),
    (
        "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
        None,
        "",
        "",
        [],
        "",
        "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
    ),
    (
        "http://registry.example.com/v2/library/ubuntu/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
        None,
        "",
        "",
        [],
        "",
        "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
    ),
    (
        "http://localhost:5000/v2/myrepo/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io",
        None,
        "",
        "",
        [],
        "",
        "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
    ),
    # A manifest url referenced by a tag falls back to the url based task id.
    (
        "http://registry.example.com/v2/library/ubuntu/manifests/latest",
        None,
        "",
        "",
        [],
        "",
        "0b6500b31c7eea2929393e154299bc81ebbb613c24fa7f5f33e893d585f4d629",
    ),
]


@pytest.mark.parametrize(
    ("url", "piece_length", "tag", "application", "filtered_query_params", "content", "expected"),
    TASK_ID_CASES,
)
def test_task_id_matches_go_and_rust(
    url: str,
    piece_length: int | None,
    tag: str,
    application: str,
    filtered_query_params: list[str],
    content: str,
    expected: str,
) -> None:
    assert (
        task_id(
            url,
            piece_length,
            tag,
            application,
            filtered_query_params,
            content,
            True,
        )
        == expected
    )


MOCK_SEED_PEERS = ["seed-peer-1", "seed-peer-2", "seed-peer-3"]
MOCK_SEED_CLIENTS = [
    "default-seed-client-0",
    "default-seed-client-1",
    "default-seed-client-2",
]

HASHRING_CASES = [
    (3, MOCK_SEED_PEERS, "task-a", 2, ["seed-peer-3|2", "seed-peer-1|2", "seed-peer-3|0"]),
    (3, MOCK_SEED_PEERS, "task-b", 2, ["seed-peer-2|0", "seed-peer-2|1", "seed-peer-1|0"]),
    (3, MOCK_SEED_PEERS, "task-c", 2, ["seed-peer-2|1", "seed-peer-1|0", "seed-peer-3|2"]),
    (
        3,
        MOCK_SEED_PEERS,
        "task-a",
        100,
        [
            "seed-peer-3|2",
            "seed-peer-1|2",
            "seed-peer-3|0",
            "seed-peer-1|1",
            "seed-peer-2|2",
            "seed-peer-3|1",
            "seed-peer-2|0",
            "seed-peer-2|1",
            "seed-peer-1|0",
        ],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000000000",
        2,
        ["default-seed-client-1|260", "default-seed-client-0|383", "default-seed-client-0|281"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000001eef",
        2,
        ["default-seed-client-1|375", "default-seed-client-2|109", "default-seed-client-0|483"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000003dde",
        2,
        ["default-seed-client-2|366", "default-seed-client-0|374", "default-seed-client-1|30"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000005ccd",
        2,
        ["default-seed-client-0|69", "default-seed-client-0|121", "default-seed-client-2|243"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000007bbc",
        2,
        ["default-seed-client-0|239", "default-seed-client-1|156", "default-seed-client-1|157"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000009aab",
        2,
        ["default-seed-client-1|501", "default-seed-client-0|435", "default-seed-client-1|308"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "000000000000000000000000000000000000000000000000000000000000b99a",
        2,
        ["default-seed-client-0|223", "default-seed-client-0|9", "default-seed-client-2|83"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "000000000000000000000000000000000000000000000000000000000000d889",
        2,
        ["default-seed-client-1|429", "default-seed-client-2|220", "default-seed-client-1|220"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "000000000000000000000000000000000000000000000000000000000000f778",
        2,
        ["default-seed-client-2|326", "default-seed-client-1|155", "default-seed-client-0|473"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000011667",
        2,
        ["default-seed-client-0|293", "default-seed-client-1|334", "default-seed-client-2|199"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000013556",
        2,
        ["default-seed-client-2|246", "default-seed-client-2|65", "default-seed-client-0|369"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "0000000000000000000000000000000000000000000000000000000000015445",
        2,
        ["default-seed-client-2|480", "default-seed-client-1|24", "default-seed-client-1|147"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "",
        2,
        ["default-seed-client-1|163", "default-seed-client-2|24", "default-seed-client-1|252"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "a",
        2,
        ["default-seed-client-0|392", "default-seed-client-1|40", "default-seed-client-0|359"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "00000",
        2,
        ["default-seed-client-2|115", "default-seed-client-2|286", "default-seed-client-1|355"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "dragonfly",
        2,
        ["default-seed-client-1|397", "default-seed-client-0|422", "default-seed-client-2|153"],
    ),
    (
        DEFAULT_VNODES_PER_NODE,
        MOCK_SEED_CLIENTS,
        "dragonfly-seed-client-0",
        2,
        ["default-seed-client-2|477", "default-seed-client-0|157", "default-seed-client-0|199"],
    ),
]


@pytest.mark.parametrize(
    ("vnodes_per_node", "nodes", "key", "replicas", "expected"), HASHRING_CASES
)
def test_hashring_matches_go_and_rust(
    vnodes_per_node: int, nodes: list[str], key: str, replicas: int, expected: list[str]
) -> None:
    ring = VNodeHashRing(vnodes_per_node=vnodes_per_node)
    for name in nodes:
        ring.add(name)

    vnode = ring.get(key)
    assert vnode is not None

    vnodes = ring.get_with_replicas(key, replicas)
    assert vnodes[0] == vnode
    assert [str(vnode) for vnode in vnodes] == expected
