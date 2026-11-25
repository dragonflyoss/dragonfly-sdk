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

"""Generates the ids of the resources, identical to the client's task id
generation. The task id decides which seed peers serve a task, so it is a wire
compatibility contract shared with the Rust crate and the Go module.
"""

from __future__ import annotations

import hashlib
from collections.abc import Sequence

from .digest import Digest, is_blob_url, is_manifest_digest_url
from .errors import InvalidArgumentError
from .url import filter_query_params

__all__ = [
    "CONTAINERD_FILTERED_QUERY_PARAMS",
    "COS_FILTERED_QUERY_PARAMS",
    "DEFAULT_FILTERED_QUERY_PARAMS",
    "GCS_FILTERED_QUERY_PARAMS",
    "OBS_FILTERED_QUERY_PARAMS",
    "OSS_FILTERED_QUERY_PARAMS",
    "S3_FILTERED_QUERY_PARAMS",
    "IDGenerator",
    "task_id",
    "task_id_by_blob_digest",
    "task_id_by_content",
    "task_id_by_manifest_digest",
    "task_id_by_url",
]

# The task type the url based task id is generated for, the string name of
# TaskType.STANDARD in the dragonfly api.
_STANDARD_TASK_TYPE = "STANDARD"

# S3_FILTERED_QUERY_PARAMS is the default filtered query params with s3 protocol
# to generate the task id.
S3_FILTERED_QUERY_PARAMS = (
    "X-Amz-Algorithm",
    "X-Amz-Credential",
    "X-Amz-Date",
    "X-Amz-Expires",
    "X-Amz-SignedHeaders",
    "X-Amz-Signature",
    "X-Amz-Security-Token",
    "X-Amz-User-Agent",
)

# GCS_FILTERED_QUERY_PARAMS is the filtered query params with gcs protocol to
# generate the task id.
GCS_FILTERED_QUERY_PARAMS = (
    "X-Goog-Algorithm",
    "X-Goog-Credential",
    "X-Goog-Date",
    "X-Goog-Expires",
    "X-Goog-SignedHeaders",
    "X-Goog-Signature",
)

# OSS_FILTERED_QUERY_PARAMS is the default filtered query params with oss
# protocol to generate the task id.
OSS_FILTERED_QUERY_PARAMS = (
    "OSSAccessKeyId",
    "Expires",
    "Signature",
    "SecurityToken",
)

# OBS_FILTERED_QUERY_PARAMS is the default filtered query params with obs
# protocol to generate the task id.
OBS_FILTERED_QUERY_PARAMS = (
    "AccessKeyId",
    "Signature",
    "Expires",
    "X-Obs-Date",
    "X-Obs-Security-Token",
)

# COS_FILTERED_QUERY_PARAMS is the default filtered query params with cos
# protocol to generate the task id.
COS_FILTERED_QUERY_PARAMS = (
    "q-sign-algorithm",
    "q-ak",
    "q-sign-time",
    "q-key-time",
    "q-header-list",
    "q-url-param-list",
    "q-signature",
    "x-cos-security-token",
)

# CONTAINERD_FILTERED_QUERY_PARAMS is the default filtered query params with
# containerd to generate the task id.
CONTAINERD_FILTERED_QUERY_PARAMS = ("ns",)

# DEFAULT_FILTERED_QUERY_PARAMS is the default filtered query params to generate
# the task id.
DEFAULT_FILTERED_QUERY_PARAMS = (
    *S3_FILTERED_QUERY_PARAMS,
    *GCS_FILTERED_QUERY_PARAMS,
    *OSS_FILTERED_QUERY_PARAMS,
    *OBS_FILTERED_QUERY_PARAMS,
    *COS_FILTERED_QUERY_PARAMS,
    *CONTAINERD_FILTERED_QUERY_PARAMS,
)


def _sha256_from_strings(*parts: str) -> str:
    hasher = hashlib.sha256()
    for part in parts:
        hasher.update(part.encode())

    return hasher.hexdigest()


def task_id_by_content(content: str) -> str:
    """Generates the v2 version of the task id by content."""
    return _sha256_from_strings(content)


def task_id_by_url(
    url: str,
    piece_length: int | None = None,
    tag: str = "",
    application: str = "",
    filtered_query_params: Sequence[str] | None = None,
    revision: str = "",
) -> str:
    """Generates the v2 version of the task id by the url based parameters.

    The revision distinguishes an artifact served from the same url at different
    revisions, such as a git repository.
    """
    final_url = filter_query_params(url, filtered_query_params or ())

    parts = [final_url, tag, application, revision]
    if piece_length is not None:
        parts.append(str(piece_length))

    parts.append(_STANDARD_TASK_TYPE)
    return _sha256_from_strings(*parts)


def task_id_by_blob_digest(url: str) -> str:
    """Generates the v2 version of the task id by the digest extracted from the
    OCI blob url.
    """
    digest = Digest.extract_from_blob_url(url)
    if digest is None:
        raise InvalidArgumentError(f"invalid blob url: {url}")

    return digest.encoded


def task_id_by_manifest_digest(url: str) -> str:
    """Generates the v2 version of the task id by the digest extracted from the
    OCI manifest url.
    """
    digest = Digest.extract_from_manifest_url(url)
    if digest is None:
        raise InvalidArgumentError(f"invalid manifest url: {url}")

    return digest.encoded


def task_id(
    url: str,
    piece_length: int | None = None,
    tag: str = "",
    application: str = "",
    filtered_query_params: Sequence[str] | None = None,
    content_for_calculating_task_id: str = "",
    enable_task_id_based_blob_digest: bool = True,
) -> str:
    """Generates the v2 version of the task id, selecting the generator by the
    parameters, identical to the client's task id generation.

    If the content is not empty, generate the task id by the content. If the task
    id based blob digest is enabled and the url is an OCI blob url or an OCI
    manifest url referenced by a digest, generate the task id by the digest.
    Otherwise, generate the task id by the url based parameters.
    """
    if content_for_calculating_task_id:
        return task_id_by_content(content_for_calculating_task_id)

    if enable_task_id_based_blob_digest and is_blob_url(url):
        return task_id_by_blob_digest(url)

    if enable_task_id_based_blob_digest and is_manifest_digest_url(url):
        return task_id_by_manifest_digest(url)

    return task_id_by_url(url, piece_length, tag, application, filtered_query_params)


class IDGenerator:
    """Used to generate the id for the resources."""

    def __init__(self, ip: str, hostname: str, is_seed_peer: bool = False) -> None:
        self._ip = ip
        self._hostname = hostname
        self._is_seed_peer = is_seed_peer

    def host_id(self) -> str:
        """Generates the host id."""
        if self._is_seed_peer:
            return f"{self._ip}-{self._hostname}-seed"

        return f"{self._ip}-{self._hostname}"
