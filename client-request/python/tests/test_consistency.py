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
# (client-request/go/consistency_test.go) and the Rust SDK
# (client-request/rust/tests/consistency.rs). All three test suites assert the
# same fixed outputs, so a change on any side that breaks task id compatibility
# fails the build.

import pytest

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
