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

# Mirrors the Rust crate's src/digest.rs unit tests and d7y.io/dragonfly/v2
# pkg/idgen task_id_test.go TestIsBlobURL / TestIsManifestDigestURL.

import pytest

from dragonfly_client_request.digest import (
    Algorithm,
    Digest,
    is_blob_url,
    is_manifest_digest_url,
)

SHA256_DIGEST = "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
SHA512_DIGEST = "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8"


# Rust digest.rs extracts_digest_from_blob_url (195-233).
@pytest.mark.parametrize(
    ("url", "expected"),
    [
        (
            f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST}",
            Digest(Algorithm.SHA256, SHA256_DIGEST),
        ),
        (
            f"https://registry.example.com/v2/myorg/myrepo/blobs/sha512:{SHA512_DIGEST}",
            Digest(Algorithm.SHA512, SHA512_DIGEST),
        ),
        (
            f"https://registry.io/v2/org/team/project/blobs/sha256:{SHA256_DIGEST}",
            Digest(Algorithm.SHA256, SHA256_DIGEST),
        ),
        (
            f"http://localhost:5000/v2/myrepo/blobs/sha256:{SHA256_DIGEST}?ns=docker.io",
            Digest(Algorithm.SHA256, SHA256_DIGEST),
        ),
        ("http://registry.example.com/blobs/sha256:abc", None),
        ("http://registry.example.com/v2/repo/manifests/sha256:abc", None),
        ("registry.example.com/v2/repo/blobs/sha256:abc", None),
        ("http://registry.example.com/v2/blobs/sha256:abc", None),
        ("", None),
        ("not-a-url", None),
        ("http://registry.example.com/v2/repo/blobs/invalid-digest", None),
        ("http://registry.example.com/v2/repo/blobs/md5:8a04994a666b4e4b20a2fd9e5a44f44c", None),
    ],
)
def test_extract_from_blob_url(url: str, expected: Digest | None) -> None:
    assert Digest.extract_from_blob_url(url) == expected


# Go task_id_test.go TestIsBlobURL (236-289).
@pytest.mark.parametrize(
    ("url", "expected"),
    [
        (f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST}", True),
        (f"https://registry.io/v2/org/team/project/blobs/sha256:{SHA256_DIGEST}", True),
        (f"http://localhost:5000/v2/myrepo/blobs/sha256:{SHA256_DIGEST}?ns=docker.io", True),
        ("https://registry.example.com/v2/library/ubuntu/manifests/latest", False),
        ("https://example.com/file.txt", False),
    ],
)
def test_is_blob_url(url: str, expected: bool) -> None:
    assert is_blob_url(url) == expected


# Rust digest.rs recognizes_manifest_digest_url (236-268) and Go TestIsManifestDigestURL (291-360).
@pytest.mark.parametrize(
    ("url", "expected"),
    [
        (f"http://registry.example.com/v2/library/ubuntu/manifests/sha256:{SHA256_DIGEST}", True),
        (f"https://registry.io/v2/org/team/project/manifests/sha256:{SHA256_DIGEST}", True),
        (f"http://localhost:5000/v2/myrepo/manifests/sha256:{SHA256_DIGEST}?ns=docker.io", True),
        ("http://registry.example.com/v2/library/ubuntu/manifests/latest", False),
        (
            "http://registry.example.com/v2/library/ubuntu/manifests/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
            False,
        ),
        ("http://registry.example.com/v2/library/ubuntu/manifests/sha256:abc", False),
        (f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST}", False),
        ("https://example.com/file.txt", False),
    ],
)
def test_is_manifest_digest_url(url: str, expected: bool) -> None:
    assert is_manifest_digest_url(url) == expected


# Rust digest.rs extracts_digest_from_manifest_url (271-310).
@pytest.mark.parametrize(
    ("url", "expected"),
    [
        (
            f"http://registry.example.com/v2/library/ubuntu/manifests/sha256:{SHA256_DIGEST}",
            Digest(Algorithm.SHA256, SHA256_DIGEST),
        ),
        (
            f"http://localhost:5000/v2/myrepo/manifests/sha256:{SHA256_DIGEST}?ns=docker.io",
            Digest(Algorithm.SHA256, SHA256_DIGEST),
        ),
        ("http://registry.example.com/v2/library/ubuntu/manifests/latest", None),
        ("http://registry.example.com/v2/library/ubuntu/manifests/sha256:abc", None),
        (
            "http://registry.example.com/v2/library/ubuntu/manifests/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
            None,
        ),
        (f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST}", None),
        ("https://example.com/file.txt", None),
    ],
)
def test_extract_from_manifest_url(url: str, expected: Digest | None) -> None:
    assert Digest.extract_from_manifest_url(url) == expected


# Rust digest.rs algorithm_display (313-323) and algorithm_from_str (326-337).
@pytest.mark.parametrize(
    ("algorithm", "expected"),
    [(Algorithm.CRC32, "crc32"), (Algorithm.SHA256, "sha256"), (Algorithm.SHA512, "sha512")],
)
def test_algorithm_str(algorithm: Algorithm, expected: str) -> None:
    assert str(algorithm) == expected
    assert Algorithm(expected) is algorithm


def test_algorithm_rejects_unknown() -> None:
    with pytest.raises(ValueError):
        Algorithm("invalid")


# Rust digest.rs digest_display (340-353).
@pytest.mark.parametrize(
    ("algorithm", "encoded", "expected"),
    [
        (Algorithm.CRC32, "1475635037", "crc32:1475635037"),
        (Algorithm.SHA256, "encoded_hash", "sha256:encoded_hash"),
        (Algorithm.SHA512, "encoded_hash", "sha512:encoded_hash"),
    ],
)
def test_digest_str(algorithm: Algorithm, encoded: str, expected: str) -> None:
    assert str(Digest(algorithm, encoded)) == expected


# Digest.parse follows d7y.io/dragonfly/v2 pkg/digest Parse: surrounding
# whitespace is trimmed, exactly one separator is allowed and the encoded value
# must be hex of the length of the algorithm, so a manifest url the scheduler
# does not treat as a digest reference falls back to the url based task id here
# as well.
@pytest.mark.parametrize(
    ("s", "expected"),
    [
        ("crc32:1475635037", Digest(Algorithm.CRC32, "1475635037")),
        ("crc32:", None),
        ("crc32:zzzz", None),
        ("crc32:a:b", None),
        (f"sha256:{SHA256_DIGEST}", Digest(Algorithm.SHA256, SHA256_DIGEST)),
        (f"sha256:{SHA256_DIGEST.upper()}", Digest(Algorithm.SHA256, SHA256_DIGEST.upper())),
        (f" sha256:{SHA256_DIGEST}\n", Digest(Algorithm.SHA256, SHA256_DIGEST)),
        (f"sha256:{SHA256_DIGEST[:-1]}", None),
        (f"sha256:{SHA256_DIGEST[:-1]}g", None),
        (f"sha256:{'g' * 64}", None),
        (f"sha256:{'中' * 64}", None),
        (f"SHA256:{SHA256_DIGEST}", None),
        (f"sha256:{SHA256_DIGEST}:", None),
        (f"sha512:{SHA512_DIGEST}", Digest(Algorithm.SHA512, SHA512_DIGEST)),
        (f"sha512:{SHA512_DIGEST[:-1]}", None),
        ("md5:8a04994a666b4e4b20a2fd9e5a44f44c", None),
        ("sha1:da39a3ee5e6b4b0d3255bfef95601890afd80709", None),
        (f"blake3:{SHA256_DIGEST}", None),
        ("bar", None),
        ("", None),
    ],
)
def test_digest_parse(s: str, expected: Digest | None) -> None:
    assert Digest.parse(s) == expected


# Whitespace around the digest reference is trimmed like the scheduler trims
# it, while the url patterns are anchored at the end of the text like the Go
# and the Rust regexes, so a newline after the query ends the match.
@pytest.mark.parametrize(
    "url",
    [
        f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST}\n",
        f"http://registry.example.com/v2/library/ubuntu/blobs/ sha256:{SHA256_DIGEST}",
        f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST} ",
    ],
)
def test_extract_from_blob_url_trims_reference(url: str) -> None:
    assert is_blob_url(url)
    assert Digest.extract_from_blob_url(url) == Digest(Algorithm.SHA256, SHA256_DIGEST)


def test_url_patterns_anchor_at_end_of_text() -> None:
    url = (
        f"http://registry.example.com/v2/library/ubuntu/blobs/sha256:{SHA256_DIGEST}?ns=docker.io\n"
    )
    assert not is_blob_url(url)
    assert not is_manifest_digest_url(url)
    assert Digest.extract_from_blob_url(url) is None
    assert Digest.extract_from_manifest_url(url) is None
