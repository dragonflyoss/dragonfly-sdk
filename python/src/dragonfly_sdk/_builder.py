# Copyright 2026 The Dragonfly Authors
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

"""Validation and construction of the dfdaemon ``DownloadTaskRequest``.

The defaults and the shape of the request mirror ``dfget`` so that the SDK and
the CLI produce the same tasks in dfdaemon.
"""

from __future__ import annotations

import math
import os
from collections.abc import Sequence
from datetime import timedelta
from pathlib import Path
from typing import Any, Union, cast
from urllib.parse import urlsplit

from dragonfly_api import common_pb2, dfdaemon_pb2
from google.protobuf.duration_pb2 import Duration

from ._credentials import (
    Credentials,
    HuggingFaceCredentials,
    ModelScopeCredentials,
    ObjectStorageCredentials,
    OpenCSGCredentials,
)
from ._errors import ValidationError
from ._headers import HeadersLike, parse_headers
from ._query_params import default_filtered_query_params

STORAGE_SCHEMES = frozenset({"s3", "gs", "abs", "oss", "obs", "cos"})
HUB_SCHEMES = frozenset({"hf", "modelscope", "opencsg"})
SUPPORTED_SCHEMES = frozenset({"http", "https"}) | STORAGE_SCHEMES | HUB_SCHEMES

MIN_PIECE_LENGTH = 4 * 1024 * 1024

DEFAULT_TIMEOUT = timedelta(hours=2)
DEFAULT_PRIORITY = 6

OutputLike = Union[str, "os.PathLike[str]"]
TimeoutLike = Union[float, timedelta]
SchedulingPolicyLike = Union[str, int]


def timeout_to_seconds(timeout: TimeoutLike) -> float:
    """Validate a timeout and convert it to seconds."""
    if isinstance(timeout, timedelta):
        seconds = timeout.total_seconds()
    elif isinstance(timeout, bool) or not isinstance(timeout, (int, float)):
        raise ValidationError("timeout must be a number of seconds or a datetime.timedelta")
    else:
        seconds = float(timeout)

    if not math.isfinite(seconds) or seconds <= 0:
        raise ValidationError("timeout must be positive and finite")

    return seconds


def build_download_task_request(
    *,
    url: str,
    output: OutputLike,
    headers: HeadersLike = None,
    digest: str | None = None,
    tag: str = "",
    application: str = "",
    priority: int = DEFAULT_PRIORITY,
    timeout_seconds: float = DEFAULT_TIMEOUT.total_seconds(),
    piece_length: int | None = None,
    filtered_query_params: Sequence[str] | None = None,
    overwrite: bool = False,
    force_hard_link: bool = False,
    disable_back_to_source: bool = False,
    scheduling_policy: SchedulingPolicyLike = "auto",
    content_for_calculating_task_id: str | None = None,
    credentials: Credentials | None = None,
) -> dfdaemon_pb2.DownloadTaskRequest:
    """Validate user arguments and build a ``DownloadTaskRequest``."""
    scheme = _validate_url(url)
    output_path = _validate_output(output, overwrite=overwrite, force_hard_link=force_hard_link)

    if not isinstance(tag, str) or not isinstance(application, str):
        raise ValidationError("tag and application must be strings")

    if digest is not None and not isinstance(digest, str):
        raise ValidationError("digest must be a string or None")

    if content_for_calculating_task_id is not None and not isinstance(
        content_for_calculating_task_id, str
    ):
        raise ValidationError("content_for_calculating_task_id must be a string or None")

    _validate_priority(priority)
    _validate_piece_length(piece_length)

    filtered = _validate_filtered_query_params(filtered_query_params)
    request_header = parse_headers(headers)
    policy = _parse_scheduling_policy(scheduling_policy)

    download = common_pb2.Download(
        url=url,
        type=common_pb2.TaskType.STANDARD,
        tag=tag,
        application=application,
        # Protobuf enum fields store their integer value; the generated stubs
        # type them as enum classes, which is stricter than the runtime.
        priority=cast(Any, priority),
        filtered_query_params=filtered,
        request_header=dict(request_header),
        output_path=str(output_path),
        timeout=_to_duration(timeout_seconds),
        need_back_to_source=False,
        scheduling_policy=cast(Any, policy),
        disable_back_to_source=disable_back_to_source,
        certificate_chain=[],
        prefetch=False,
        is_prefetch=False,
        need_piece_content=False,
        force_hard_link=force_hard_link,
        overwrite=overwrite,
        enable_task_id_based_blob_digest=False,
        metadata_only=False,
    )

    # These fields are optional in the proto. Only set them when the caller
    # provided a value: writing an empty string changes field presence and can
    # alter digest and task-id semantics.
    if digest is not None:
        download.digest = digest

    if piece_length is not None:
        download.piece_length = piece_length

    if content_for_calculating_task_id is not None:
        download.content_for_calculating_task_id = content_for_calculating_task_id

    _attach_backend(download, scheme, credentials)

    return dfdaemon_pb2.DownloadTaskRequest(download=download)


def _validate_url(url: str) -> str:
    if not isinstance(url, str) or not url.strip():
        raise ValidationError("url must be a non-empty string")

    scheme = urlsplit(url).scheme.lower()
    if not scheme:
        raise ValidationError(f"url must be absolute and include a scheme: {url!r}")

    if scheme == "hdfs":
        raise ValidationError("HDFS downloads are not supported by dragonfly-sdk yet")

    if scheme not in SUPPORTED_SCHEMES:
        supported = ", ".join(sorted(SUPPORTED_SCHEMES))
        raise ValidationError(f"unsupported url scheme {scheme!r}; supported schemes: {supported}")

    if url.endswith("/"):
        raise ValidationError(
            "directory downloads are not supported; the url must point to a single file"
        )

    return scheme


def _validate_output(output: OutputLike, *, overwrite: bool, force_hard_link: bool) -> Path:
    if overwrite and force_hard_link:
        raise ValidationError("overwrite and force_hard_link cannot be used together")

    if not isinstance(output, (str, os.PathLike)):
        raise ValidationError("output must be a filesystem path")

    raw = os.fspath(output)
    if not isinstance(raw, str) or not raw:
        raise ValidationError("output must be a non-empty string path")

    # dfdaemon resolves relative paths against its own working directory, so the
    # SDK always sends an absolute path.
    output_path = Path(os.path.abspath(os.path.expanduser(raw)))

    if not output_path.parent.is_dir():
        raise ValidationError(f"output directory does not exist: {output_path.parent}")

    if output_path.exists():
        if output_path.is_dir():
            raise ValidationError(f"output path is a directory: {output_path}")

        if not overwrite:
            raise ValidationError(
                f"output file already exists: {output_path}; pass overwrite=True to replace it"
            )

    return output_path


def _validate_priority(priority: int) -> None:
    if isinstance(priority, bool) or not isinstance(priority, int):
        raise ValidationError("priority must be an integer between 0 and 6")

    if not 0 <= priority <= 6:
        raise ValidationError("priority must be an integer between 0 and 6")


def _validate_piece_length(piece_length: int | None) -> None:
    if piece_length is None:
        return

    if isinstance(piece_length, bool) or not isinstance(piece_length, int):
        raise ValidationError("piece_length must be an integer number of bytes")

    if piece_length < MIN_PIECE_LENGTH:
        raise ValidationError(
            f"piece_length {piece_length} is less than the minimum of {MIN_PIECE_LENGTH} bytes"
        )


def _validate_filtered_query_params(params: Sequence[str] | None) -> list[str]:
    if params is None:
        return default_filtered_query_params()

    if isinstance(params, (str, bytes)):
        raise ValidationError("filtered_query_params must be a sequence of strings")

    filtered: list[str] = []
    for param in params:
        if not isinstance(param, str) or not param:
            raise ValidationError("filtered_query_params must contain non-empty strings")
        filtered.append(param)

    return filtered


def _parse_scheduling_policy(policy: SchedulingPolicyLike) -> int:
    if isinstance(policy, bool):
        raise ValidationError("scheduling_policy must be 'auto', 'always', 0 or 1")

    if isinstance(policy, int):
        if policy == common_pb2.SchedulingPolicy.AUTO:
            return common_pb2.SchedulingPolicy.AUTO
        if policy == common_pb2.SchedulingPolicy.ALWAYS:
            return common_pb2.SchedulingPolicy.ALWAYS
        raise ValidationError("scheduling_policy must be 'auto', 'always', 0 or 1")

    if isinstance(policy, str):
        normalized = policy.strip().lower()
        if normalized == "auto":
            return common_pb2.SchedulingPolicy.AUTO
        if normalized == "always":
            return common_pb2.SchedulingPolicy.ALWAYS

    raise ValidationError("scheduling_policy must be 'auto', 'always', 0 or 1")


def _to_duration(seconds: float) -> Duration:
    whole = int(seconds)
    nanos = int(round((seconds - whole) * 1_000_000_000))
    if nanos >= 1_000_000_000:
        whole += 1
        nanos -= 1_000_000_000

    return Duration(seconds=whole, nanos=nanos)


def _attach_backend(
    download: common_pb2.Download,
    scheme: str,
    credentials: Credentials | None,
) -> None:
    if scheme in STORAGE_SCHEMES:
        if credentials is None:
            credentials = ObjectStorageCredentials()
        elif not isinstance(credentials, ObjectStorageCredentials):
            raise ValidationError(
                f"{scheme!r} urls require ObjectStorageCredentials, "
                f"got {type(credentials).__name__}"
            )

        download.object_storage.CopyFrom(
            common_pb2.ObjectStorage(
                region=credentials.region,
                endpoint=credentials.endpoint,
                access_key_id=credentials.access_key_id,
                access_key_secret=credentials.access_key_secret,
                session_token=credentials.session_token,
                credential_path=credentials.credential_path,
                predefined_acl=credentials.predefined_acl,
                security_token=credentials.security_token,
                insecure_skip_verify=credentials.insecure_skip_verify,
            )
        )
        return

    if scheme == "hf":
        if credentials is None:
            credentials = HuggingFaceCredentials()
        if not isinstance(credentials, HuggingFaceCredentials):
            raise ValidationError(
                f"{scheme!r} urls require HuggingFaceCredentials, got {type(credentials).__name__}"
            )
        download.hugging_face.CopyFrom(
            common_pb2.HuggingFace(
                token=credentials.token,
                revision=credentials.revision,
                base_url=credentials.base_url,
            )
        )
    elif scheme == "modelscope":
        if credentials is None:
            credentials = ModelScopeCredentials()
        if not isinstance(credentials, ModelScopeCredentials):
            raise ValidationError(
                f"{scheme!r} urls require ModelScopeCredentials, got {type(credentials).__name__}"
            )
        download.model_scope.CopyFrom(
            common_pb2.ModelScope(
                token=credentials.token,
                revision=credentials.revision,
                base_url=credentials.base_url,
            )
        )
    elif scheme == "opencsg":
        if credentials is None:
            credentials = OpenCSGCredentials()
        if not isinstance(credentials, OpenCSGCredentials):
            raise ValidationError(
                f"{scheme!r} urls require OpenCSGCredentials, got {type(credentials).__name__}"
            )
        download.open_csg.CopyFrom(
            common_pb2.OpenCSG(
                token=credentials.token,
                revision=credentials.revision,
                base_url=credentials.base_url,
            )
        )
    elif credentials is not None:
        raise ValidationError(f"credentials are not supported for {scheme!r} urls")
