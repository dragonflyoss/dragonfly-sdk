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

"""Mirrored from the dragonfly client's `dragonfly-client-util/src/digest/mod.rs`.
The digest extraction here decides the task id, so keep both copies in sync, any
divergence silently produces different task ids between the SDK and the client.
"""

from __future__ import annotations

import re
from dataclasses import dataclass
from enum import StrEnum

__all__ = ["Algorithm", "Digest", "is_blob_url", "is_manifest_digest_url"]

# The separator character for digest formatting.
SEPARATOR = ":"

# Regex pattern for OCI blob URLs, e.g. `http(s)://<registry>/v2/<repository>/blobs/<digest>`.
_BLOB_URL_REGEX = re.compile(r"^(.*)://(.*)/v2/(.*)/blobs/([^?]+)(?:\?.*)?\Z")

# Regex pattern for OCI manifest URLs, e.g. `http(s)://<registry>/v2/<repository>/manifests/<reference>`.
_MANIFEST_URL_REGEX = re.compile(r"^(.*)://(.*)/v2/(.*)/manifests/([^?]+)(?:\?.*)?\Z")


class Algorithm(StrEnum):
    """Algorithm for generating digests."""

    CRC32 = "crc32"
    SHA256 = "sha256"
    SHA512 = "sha512"


# The encoded length each algorithm requires, crc32 only requiring a non-empty
# value.
_ENCODED_LENGTHS = {Algorithm.SHA256: 64, Algorithm.SHA512: 128}

_HEX_DIGITS = frozenset("0123456789abcdefABCDEF")


@dataclass(frozen=True)
class Digest:
    """A digest value with its associated algorithm."""

    algorithm: Algorithm
    encoded: str

    def __str__(self) -> str:
        return f"{self.algorithm}{SEPARATOR}{self.encoded}"

    @classmethod
    def parse(cls, s: str) -> Digest | None:
        """Parses a digest string, returning None when it is not a digest with a
        supported algorithm and a hex encoded value of the valid length. The
        rules are the scheduler's, so a manifest url the scheduler does not
        treat as a digest reference is not treated as one here either.
        """
        parts = s.strip().split(SEPARATOR)
        if len(parts) != 2:
            return None

        algorithm, encoded = parts
        try:
            parsed = Algorithm(algorithm)
        except ValueError:
            return None

        if not encoded or any(c not in _HEX_DIGITS for c in encoded):
            return None

        expected = _ENCODED_LENGTHS.get(parsed)
        if expected is not None and len(encoded) != expected:
            return None

        return cls(parsed, encoded)

    @classmethod
    def extract_from_blob_url(cls, url: str) -> Digest | None:
        """Extracts the digest from an OCI blob URL, e.g.
        `http(s)://<registry>/v2/<repository>/blobs/<digest>`.
        """
        match = _BLOB_URL_REGEX.match(url)
        return cls.parse(match.group(4)) if match else None

    @classmethod
    def extract_from_manifest_url(cls, url: str) -> Digest | None:
        """Extracts the digest from an OCI manifest URL, e.g.
        `http(s)://<registry>/v2/<repository>/manifests/<digest>`.
        """
        match = _MANIFEST_URL_REGEX.match(url)
        return cls.parse(match.group(4)) if match else None


def is_blob_url(url: str) -> bool:
    """Checks if the URL is an OCI blob URL."""
    return _BLOB_URL_REGEX.match(url) is not None


def is_manifest_digest_url(url: str) -> bool:
    """Checks if the URL is an OCI manifest URL whose reference is a digest with
    a supported algorithm. A manifest URL referenced by a tag returns False, so
    it falls back to the url based task id.
    """
    return Digest.extract_from_manifest_url(url) is not None
