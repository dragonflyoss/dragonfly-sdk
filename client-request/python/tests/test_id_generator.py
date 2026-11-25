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

# Mirrors d7y.io/dragonfly/v2 pkg/idgen task_id_test.go and host_id_test.go,
# the Rust crate's src/id_generator.rs unit tests, and the Go SDK's
# proxy_test.go TestTaskIDBlobDigestDisabled.

import pytest

from dragonfly_client_request.errors import InvalidArgumentError
from dragonfly_client_request.id_generator import (
    IDGenerator,
    task_id,
    task_id_by_blob_digest,
    task_id_by_content,
    task_id_by_manifest_digest,
    task_id_by_url,
)

SHA256_DIGEST = "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
SHA512_DIGEST = "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8"
BLOB_URL = f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST}"
MANIFEST_URL = f"http://registry.example.com/v2/library/ubuntu/manifests/sha256:{SHA256_DIGEST}"


# Go idgen/host_id_test.go TestHostID and Rust id_generator.rs should_generate_host_id.
@pytest.mark.parametrize(
    ("ip", "hostname", "is_seed_peer", "expected"),
    [
        ("127.0.0.1", "localhost", False, "127.0.0.1-localhost"),
        ("127.0.0.1", "localhost", True, "127.0.0.1-localhost-seed"),
        ("127.0.0.1", "foo", False, "127.0.0.1-foo"),
        ("127.0.0.1", "foo", True, "127.0.0.1-foo-seed"),
        ("", "foo", True, "-foo-seed"),
        ("127.0.0.1", "", True, "127.0.0.1--seed"),
    ],
)
def test_host_id(ip: str, hostname: str, is_seed_peer: bool, expected: str) -> None:
    assert IDGenerator(ip, hostname, is_seed_peer).host_id() == expected


# Go idgen/task_id_test.go TestTaskIDV2ByURLBased (lines 88-224) and Rust
# id_generator.rs should_generate_task_id, the vectors not already in
# test_consistency.py. The revision vectors only reach task_id_by_url, since
# task_id() mirrors Go's TaskIDV2 which has no revision parameter.
@pytest.mark.parametrize(
    ("url", "piece_length", "tag", "application", "filtered_query_params", "revision", "expected"),
    [
        (
            "https://example.com",
            1024,
            "foo",
            "bar",
            [],
            "v1.0",
            "5844f27a257287e9b734256bb25603d8005422ced8c0377f15063ec11963b25f",
        ),
        (
            "https://example.com",
            None,
            "foo",
            "bar",
            [],
            "",
            "06408fbf247ddaca478f8cb9565fe5591c28efd0994b8fea80a6a87d3203c5ca",
        ),
        (
            "https://example.com",
            None,
            "foo",
            "",
            [],
            "",
            "3c3f230ef9f191dd2821510346a7bc138e4894bee9aee184ba250a3040701d2a",
        ),
        (
            "https://example.com",
            None,
            "",
            "bar",
            [],
            "",
            "c9f9261b7305c24371244f9f149f5d4589ed601348fdf22d7f6f4b10658fdba2",
        ),
        (
            "https://example.com",
            1024,
            "",
            "",
            [],
            "",
            "9f7c9aafbc6f30f8f41a96ca77eeae80c5b60964b3034b0ee43ccf7b2f9e52b8",
        ),
        (
            "https://example.com?foo=foo&bar=bar",
            None,
            "",
            "",
            ["foo", "bar"],
            "",
            "457b4328cde278e422c9e243f7bfd1e97f511fec43a80f535cf6b0ef6b086776",
        ),
        (
            "https://example.com",
            None,
            "",
            "",
            [],
            "v1.0",
            "b171331534b80e0bf91da38ebbfcdbf4d177898f4b9beac44f14733e3f004d4e",
        ),
    ],
)
def test_task_id_by_url_matches_go_and_rust(
    url: str,
    piece_length: int | None,
    tag: str,
    application: str,
    filtered_query_params: list[str],
    revision: str,
    expected: str,
) -> None:
    assert (
        task_id_by_url(url, piece_length, tag, application, filtered_query_params, revision)
        == expected
    )


def test_task_id_by_content() -> None:
    assert (
        task_id_by_content("This is a test file")
        == "e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249"
    )


# Go TestTaskIDV2ByBlobDigest (362-427) and Rust should_generate_task_id BlobDigestBased.
@pytest.mark.parametrize(
    ("url", "expected"),
    [
        (BLOB_URL, SHA256_DIGEST),
        (
            f"https://registry.example.com/v2/myorg/myrepo/blobs/sha512:{SHA512_DIGEST}",
            SHA512_DIGEST,
        ),
        (
            f"http://localhost:5000/v2/myrepo/blobs/sha256:{SHA256_DIGEST}?ns=docker.io",
            SHA256_DIGEST,
        ),
    ],
)
def test_task_id_by_blob_digest(url: str, expected: str) -> None:
    assert task_id_by_blob_digest(url) == expected
    assert task_id(url) == expected


@pytest.mark.parametrize(
    "url",
    [
        "https://example.com/file.txt",
        "http://registry.example.com/v2/library/ubuntu/blobs/sha256:abc",
        "http://registry.example.com/v2/library/ubuntu/blobs/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
    ],
)
def test_task_id_by_blob_digest_rejects(url: str) -> None:
    with pytest.raises(InvalidArgumentError):
        task_id_by_blob_digest(url)


# Go TestTaskIDV2ByManifestDigest (429-494) and Rust ManifestDigestBased.
@pytest.mark.parametrize(
    ("url", "expected"),
    [
        (MANIFEST_URL, SHA256_DIGEST),
        (
            f"https://registry.example.com/v2/myorg/myrepo/manifests/sha512:{SHA512_DIGEST}",
            SHA512_DIGEST,
        ),
        (
            f"http://localhost:5000/v2/myrepo/manifests/sha256:{SHA256_DIGEST}?ns=docker.io",
            SHA256_DIGEST,
        ),
    ],
)
def test_task_id_by_manifest_digest(url: str, expected: str) -> None:
    assert task_id_by_manifest_digest(url) == expected
    assert task_id(url) == expected


@pytest.mark.parametrize(
    "url",
    [
        "https://example.com/file.txt",
        "http://registry.example.com/v2/library/ubuntu/manifests/latest",
        "http://registry.example.com/v2/library/ubuntu/manifests/sha256:abc",
        "http://registry.example.com/v2/library/ubuntu/manifests/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
    ],
)
def test_task_id_by_manifest_digest_rejects(url: str) -> None:
    with pytest.raises(InvalidArgumentError):
        task_id_by_manifest_digest(url)


# Go TestTaskIDV2 (496-590): the dispatcher picks the generator by parameters.
def test_task_id_prefers_content_over_blob_url() -> None:
    assert task_id(
        BLOB_URL, None, "foo", "bar", None, "This is a content", True
    ) == task_id_by_content("This is a content")


@pytest.mark.parametrize(
    ("url", "enable_task_id_based_blob_digest"),
    [
        (MANIFEST_URL, False),
        ("http://registry.example.com/v2/library/ubuntu/manifests/latest", True),
        (BLOB_URL, False),
        ("https://example.com/file.txt", True),
    ],
)
def test_task_id_falls_back_to_url_based(url: str, enable_task_id_based_blob_digest: bool) -> None:
    assert task_id(
        url, None, "foo", "bar", None, "", enable_task_id_based_blob_digest
    ) == task_id_by_url(url, None, "foo", "bar", None, "")


def test_task_id_rejects_unsupported_digest_algorithm() -> None:
    with pytest.raises(InvalidArgumentError):
        task_id(
            "http://registry.example.com/v2/library/ubuntu/blobs/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
            None,
            "foo",
            "bar",
            None,
            "",
            True,
        )


# Go SDK proxy_test.go TestTaskIDBlobDigestDisabled (665-672).
def test_task_id_blob_digest_disabled() -> None:
    assert task_id(BLOB_URL, None, "", "", None, "", False) != SHA256_DIGEST
    assert task_id(BLOB_URL, None, "", "", None, "", False) == task_id_by_url(BLOB_URL)
