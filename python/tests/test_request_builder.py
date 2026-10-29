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

from pathlib import Path

import pytest
from dragonfly_api import common_pb2

from dragonfly_sdk import (
    HuggingFaceCredentials,
    ModelScopeCredentials,
    ObjectStorageCredentials,
    OpenCSGCredentials,
)
from dragonfly_sdk._query_params import DEFAULT_FILTERED_QUERY_PARAMS
from helpers import build_request


def test_dfget_defaults(tmp_path: Path) -> None:
    download = build_request(tmp_path / "out.bin").download

    assert download.url == "https://example.com/file.bin"
    assert download.type == common_pb2.TaskType.STANDARD
    assert download.priority == 6
    assert download.tag == ""
    assert download.application == ""
    assert download.need_piece_content is False
    assert download.need_back_to_source is False
    assert download.prefetch is False
    assert download.is_prefetch is False
    assert download.metadata_only is False
    assert download.enable_task_id_based_blob_digest is False
    assert download.overwrite is False
    assert download.force_hard_link is False
    assert download.disable_back_to_source is False
    assert download.scheduling_policy == common_pb2.SchedulingPolicy.AUTO


def test_optional_fields_are_absent_by_default(tmp_path: Path) -> None:
    download = build_request(tmp_path / "out.bin").download

    assert not download.HasField("digest")
    assert not download.HasField("piece_length")
    assert not download.HasField("content_for_calculating_task_id")
    assert not download.HasField("remote_ip")
    assert not download.HasField("concurrent_piece_count")
    assert download.request_header == {}


def test_optional_fields_are_set_when_provided(tmp_path: Path) -> None:
    download = build_request(
        tmp_path / "out.bin",
        digest="sha256:abc",
        piece_length=8 * 1024 * 1024,
        content_for_calculating_task_id="extra",
    ).download

    assert download.digest == "sha256:abc"
    assert download.piece_length == 8 * 1024 * 1024
    assert download.content_for_calculating_task_id == "extra"


def test_timeout_is_converted_to_a_duration(tmp_path: Path) -> None:
    download = build_request(tmp_path / "out.bin", timeout_seconds=1.5).download

    assert download.timeout.seconds == 1
    assert download.timeout.nanos == 500_000_000


def test_output_path_is_absolute(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.chdir(tmp_path)
    download = build_request("relative.bin").download

    assert Path(download.output_path).is_absolute()
    assert Path(download.output_path) == tmp_path / "relative.bin"


def test_headers_are_parsed(tmp_path: Path) -> None:
    download = build_request(
        tmp_path / "out.bin",
        headers=["Authorization: Bearer token", "Host:localhost:8080"],
    ).download

    assert dict(download.request_header) == {
        "Authorization": "Bearer token",
        "Host": "localhost:8080",
    }


def test_default_filtered_query_params(tmp_path: Path) -> None:
    download = build_request(tmp_path / "out.bin").download

    assert tuple(download.filtered_query_params) == DEFAULT_FILTERED_QUERY_PARAMS


def test_filtered_query_params_can_be_replaced(tmp_path: Path) -> None:
    download = build_request(
        tmp_path / "out.bin", filtered_query_params=["signature", "timeout"]
    ).download

    assert tuple(download.filtered_query_params) == ("signature", "timeout")


@pytest.mark.parametrize(
    "url, field",
    [
        ("s3://bucket/key", "object_storage"),
        ("gs://bucket/key", "object_storage"),
        ("oss://bucket/key", "object_storage"),
        ("hf://owner/repo/file", "hugging_face"),
        ("modelscope://owner/repo/file", "model_scope"),
        ("opencsg://owner/repo/file", "open_csg"),
    ],
)
def test_backend_message_is_attached(tmp_path: Path, url: str, field: str) -> None:
    download = build_request(tmp_path / "out.bin", url=url).download

    assert download.HasField(field)


@pytest.mark.parametrize("url", ["https://example.com/a", "http://example.com/a"])
def test_http_has_no_backend_message(tmp_path: Path, url: str) -> None:
    download = build_request(tmp_path / "out.bin", url=url).download

    assert not download.HasField("object_storage")
    assert not download.HasField("hugging_face")
    assert not download.HasField("model_scope")
    assert not download.HasField("open_csg")


def test_object_storage_defaults(tmp_path: Path) -> None:
    download = build_request(tmp_path / "out.bin", url="s3://bucket/key").download

    assert download.object_storage.predefined_acl == "publicRead"
    assert download.object_storage.region == ""
    assert download.object_storage.access_key_id == ""


def test_object_storage_credentials_are_propagated(tmp_path: Path) -> None:
    credentials = ObjectStorageCredentials(
        region="us-east-1",
        endpoint="https://s3.example.com",
        access_key_id="key",
        access_key_secret="secret",
        session_token="session",
        credential_path="/etc/creds",
        predefined_acl="private",
        security_token="security",
        insecure_skip_verify=True,
    )
    download = build_request(
        tmp_path / "out.bin", url="s3://bucket/key", credentials=credentials
    ).download

    storage = download.object_storage
    assert storage.region == "us-east-1"
    assert storage.endpoint == "https://s3.example.com"
    assert storage.access_key_id == "key"
    assert storage.access_key_secret == "secret"
    assert storage.session_token == "session"
    assert storage.credential_path == "/etc/creds"
    assert storage.predefined_acl == "private"
    assert storage.security_token == "security"
    assert storage.insecure_skip_verify is True


def test_hub_default_revisions(tmp_path: Path) -> None:
    hf = build_request(tmp_path / "out.bin", url="hf://owner/repo/f").download
    ms = build_request(tmp_path / "out.bin", url="modelscope://owner/repo/f").download
    csg = build_request(tmp_path / "out.bin", url="opencsg://owner/repo/f").download

    assert hf.hugging_face.revision == "main"
    assert ms.model_scope.revision == "master"
    assert csg.open_csg.revision == "main"


def test_hub_credentials_are_propagated(tmp_path: Path) -> None:
    hf = build_request(
        tmp_path / "out.bin",
        url="hf://owner/repo/f",
        credentials=HuggingFaceCredentials(
            token="hf-token", revision="release-v1", base_url="https://hf.example.com"
        ),
    ).download
    assert hf.hugging_face.token == "hf-token"
    assert hf.hugging_face.revision == "release-v1"
    assert hf.hugging_face.base_url == "https://hf.example.com"

    ms = build_request(
        tmp_path / "out.bin",
        url="modelscope://owner/repo/f",
        credentials=ModelScopeCredentials(token="ms-token", revision="dev"),
    ).download
    assert ms.model_scope.token == "ms-token"
    assert ms.model_scope.revision == "dev"

    csg = build_request(
        tmp_path / "out.bin",
        url="opencsg://owner/repo/f",
        credentials=OpenCSGCredentials(token="csg-token", revision="dev"),
    ).download
    assert csg.open_csg.token == "csg-token"
    assert csg.open_csg.revision == "dev"


@pytest.mark.parametrize("policy", ["auto", "AUTO", " Auto ", 0])
def test_scheduling_policy_auto(tmp_path: Path, policy: object) -> None:
    download = build_request(tmp_path / "out.bin", scheduling_policy=policy).download
    assert download.scheduling_policy == common_pb2.SchedulingPolicy.AUTO


@pytest.mark.parametrize("policy", ["always", "ALWAYS", 1])
def test_scheduling_policy_always(tmp_path: Path, policy: object) -> None:
    download = build_request(tmp_path / "out.bin", scheduling_policy=policy).download
    assert download.scheduling_policy == common_pb2.SchedulingPolicy.ALWAYS


def test_flags_and_metadata_are_set(tmp_path: Path) -> None:
    download = build_request(
        tmp_path / "out.bin",
        overwrite=True,
        force_hard_link=False,
        disable_back_to_source=True,
        priority=2,
        tag="my-app",
        application="my-app",
    ).download

    assert download.overwrite is True
    assert download.disable_back_to_source is True
    assert download.priority == 2
    assert download.tag == "my-app"
    assert download.application == "my-app"
