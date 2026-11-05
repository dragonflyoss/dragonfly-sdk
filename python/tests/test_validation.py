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

from __future__ import annotations

import math
from datetime import timedelta
from pathlib import Path

import pytest

from dragonfly_sdk import (
    HuggingFaceCredentials,
    ObjectStorageCredentials,
    ValidationError,
)
from dragonfly_sdk._builder import build_download_task_request, timeout_to_seconds
from helpers import build_request


def test_timeout_accepts_timedelta() -> None:
    assert timeout_to_seconds(timedelta(hours=2)) == 7200.0


def test_timeout_accepts_number() -> None:
    assert timeout_to_seconds(1.5) == 1.5
    assert timeout_to_seconds(60) == 60.0


@pytest.mark.parametrize("timeout", [0, -1, math.inf, -math.inf, math.nan])
def test_timeout_rejects_non_positive_or_non_finite(timeout: float) -> None:
    with pytest.raises(ValidationError):
        timeout_to_seconds(timeout)


@pytest.mark.parametrize("timeout", [True, "60", object()])
def test_timeout_rejects_wrong_types(timeout: object) -> None:
    with pytest.raises(ValidationError):
        timeout_to_seconds(timeout)  # type: ignore[arg-type]


def test_output_parent_must_exist(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="output directory does not exist"):
        build_request(tmp_path / "missing" / "out.bin")


def test_existing_output_requires_overwrite(tmp_path: Path) -> None:
    output = tmp_path / "out.bin"
    output.write_bytes(b"existing")

    with pytest.raises(ValidationError, match="overwrite=True"):
        build_request(output)

    assert build_request(output, overwrite=True).download.output_path == str(output)


def test_output_cannot_be_a_directory(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="is a directory"):
        build_request(tmp_path, overwrite=True)


def test_overwrite_and_force_hard_link_conflict(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="cannot be used together"):
        build_request(tmp_path / "out.bin", overwrite=True, force_hard_link=True)


@pytest.mark.parametrize("piece_length", [0, 1024, 4 * 1024 * 1024 - 1])
def test_piece_length_minimum(tmp_path: Path, piece_length: int) -> None:
    with pytest.raises(ValidationError, match="minimum"):
        build_request(tmp_path / "out.bin", piece_length=piece_length)


@pytest.mark.parametrize("piece_length", [True, 4 * 1024 * 1024.0, "4MiB"])
def test_piece_length_must_be_an_integer(tmp_path: Path, piece_length: object) -> None:
    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", piece_length=piece_length)  # type: ignore[arg-type]


def test_piece_length_accepts_minimum(tmp_path: Path) -> None:
    request = build_request(tmp_path / "out.bin", piece_length=4 * 1024 * 1024)
    assert request.download.piece_length == 4 * 1024 * 1024


def test_directory_url_is_rejected(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="directory"):
        build_request(tmp_path / "out", url="https://example.com/models/")


def test_unsupported_scheme_is_rejected(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="unsupported url scheme"):
        build_request(tmp_path / "out.bin", url="ftp://example.com/file.bin")


def test_hdfs_is_rejected(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="HDFS"):
        build_request(tmp_path / "out.bin", url="hdfs://namenode/file.bin")


@pytest.mark.parametrize("url", ["", "   ", "example.com/file.bin", "/tmp/file.bin"])
def test_invalid_urls_are_rejected(tmp_path: Path, url: str) -> None:
    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", url=url)


def test_non_string_url_is_rejected(tmp_path: Path) -> None:
    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", url=42)  # type: ignore[arg-type]


@pytest.mark.parametrize("priority", [-1, 7, True, 3.0])
def test_invalid_priority(tmp_path: Path, priority: object) -> None:
    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", priority=priority)  # type: ignore[arg-type]


@pytest.mark.parametrize("policy", ["", "sometimes", 2, True, None])
def test_invalid_scheduling_policy(tmp_path: Path, policy: object) -> None:
    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", scheduling_policy=policy)  # type: ignore[arg-type]


def test_filtered_query_params_must_be_a_sequence(tmp_path: Path) -> None:
    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", filtered_query_params="signature")

    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", filtered_query_params=["ok", ""])

    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", filtered_query_params=[1])


def test_object_storage_credentials_require_storage_scheme(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="not supported"):
        build_request(
            tmp_path / "out.bin",
            url="https://example.com/file.bin",
            credentials=ObjectStorageCredentials(),
        )


def test_storage_urls_require_object_storage_credentials(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="ObjectStorageCredentials"):
        build_request(
            tmp_path / "out.bin",
            url="s3://bucket/key",
            credentials=HuggingFaceCredentials(),
        )


def test_hf_urls_require_hugging_face_credentials(tmp_path: Path) -> None:
    with pytest.raises(ValidationError, match="HuggingFaceCredentials"):
        build_request(
            tmp_path / "out.bin",
            url="hf://owner/repo/file",
            credentials=ObjectStorageCredentials(),
        )


def test_tag_and_application_must_be_strings(tmp_path: Path) -> None:
    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", tag=None)  # type: ignore[arg-type]

    with pytest.raises(ValidationError):
        build_request(tmp_path / "out.bin", application=1)  # type: ignore[arg-type]


def test_build_rejects_non_path_output() -> None:
    with pytest.raises(ValidationError):
        build_download_task_request(url="https://example.com/a", output=1)  # type: ignore[arg-type]
