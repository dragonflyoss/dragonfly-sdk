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

import shutil
import tempfile
import threading
from pathlib import Path

import grpc
import pytest
from dragonfly_api import common_pb2

from dragonfly_sdk import (
    Client,
    ClientClosedError,
    ConnectionFailedError,
    DeadlineExceededError,
    DfdaemonError,
    DownloadCancelledError,
    DownloadProgress,
    ValidationError,
)
from dragonfly_sdk._client import _DEFAULT_CHANNEL_OPTIONS
from helpers import FakeDfdaemon, StreamAbort, full_download


def test_missing_socket_is_reported(tmp_path: Path) -> None:
    with pytest.raises(ConnectionFailedError, match="socket not found"):
        Client(endpoint=tmp_path / "missing.sock")


def test_unreachable_socket_times_out() -> None:
    directory = Path(tempfile.mkdtemp(prefix="dfd", dir="/tmp"))
    socket_path = directory / "dfdaemon.sock"
    socket_path.touch()

    try:
        with pytest.raises(ConnectionFailedError, match="timed out"):
            Client(endpoint=socket_path, connect_timeout=0.3)
    finally:
        shutil.rmtree(directory, ignore_errors=True)


def test_invalid_connect_timeout(tmp_path: Path) -> None:
    with pytest.raises(ValidationError):
        Client(endpoint=tmp_path / "missing.sock", connect_timeout=0)


def test_close_is_idempotent(fake_daemon: FakeDfdaemon) -> None:
    client = Client(endpoint=fake_daemon.socket_path)
    assert client.closed is False

    client.close()
    client.close()

    assert client.closed is True


def test_context_manager_closes(fake_daemon: FakeDfdaemon) -> None:
    with Client(endpoint=fake_daemon.socket_path) as client:
        assert client.closed is False

    assert client.closed is True


def test_download_after_close_raises(client: Client, tmp_path: Path) -> None:
    client.close()

    with pytest.raises(ClientClosedError):
        client.download("https://example.com/file.bin", tmp_path / "out.bin")


def test_download_end_to_end(
    client: Client,
    fake_daemon: FakeDfdaemon,
    tmp_path: Path,
) -> None:
    output = tmp_path / "out.bin"

    result = client.download("https://example.com/file.bin", output)

    assert result.task_id == "task-1"
    assert result.peer_id == "peer-1"
    assert result.host_id == "host-1"
    assert result.output_path == output
    assert result.content_length == 10
    assert result.completed_pieces == 2
    assert result.downloaded_bytes == 10

    assert len(fake_daemon.requests) == 1
    download = fake_daemon.requests[0].download
    assert download.url == "https://example.com/file.bin"
    assert download.type == common_pb2.TaskType.STANDARD
    assert download.output_path == str(output)
    assert download.need_piece_content is False
    assert download.priority == 6


def test_endpoint_property(client: Client, fake_daemon: FakeDfdaemon) -> None:
    assert client.endpoint == fake_daemon.socket_path
    assert repr(client).startswith("Client(endpoint=")


def test_progress_callback(
    client: Client,
    tmp_path: Path,
) -> None:
    events: list[DownloadProgress] = []

    client.download(
        "https://example.com/file.bin",
        tmp_path / "out.bin",
        on_progress=events.append,
    )

    assert [event.percent for event in events] == [0.0, 50.0, 100.0]
    assert all(event.output_path == tmp_path / "out.bin" for event in events)
    assert all(event.url == "https://example.com/file.bin" for event in events)


def test_validation_happens_before_the_request(
    client: Client,
    fake_daemon: FakeDfdaemon,
    tmp_path: Path,
) -> None:
    with pytest.raises(ValidationError):
        client.download(
            "https://example.com/file.bin",
            tmp_path / "out.bin",
            piece_length=1,
        )

    assert fake_daemon.requests == []


def test_existing_output_requires_overwrite(
    client: Client,
    tmp_path: Path,
) -> None:
    output = tmp_path / "out.bin"
    output.write_bytes(b"existing")

    with pytest.raises(ValidationError, match="overwrite=True"):
        client.download("https://example.com/file.bin", output)

    result = client.download("https://example.com/file.bin", output, overwrite=True)
    assert result.output_path == output


def test_concurrent_downloads(client: Client, fake_daemon: FakeDfdaemon, tmp_path: Path) -> None:
    results: list[object] = []
    errors: list[BaseException] = []

    def run(index: int) -> None:
        try:
            results.append(
                client.download(
                    f"https://example.com/file-{index}.bin",
                    tmp_path / f"out-{index}.bin",
                )
            )
        except BaseException as exc:  # pragma: no cover - surfaced below
            errors.append(exc)

    threads = [threading.Thread(target=run, args=(index,)) for index in range(4)]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()

    assert errors == []
    assert len(results) == 4
    assert len(fake_daemon.requests) == 4


def test_deadline_error_is_translated(
    client: Client,
    fake_daemon: FakeDfdaemon,
    tmp_path: Path,
) -> None:
    def responder(request):  # type: ignore[no-untyped-def]
        raise StreamAbort(grpc.StatusCode.DEADLINE_EXCEEDED, "too slow")

    fake_daemon.responder = responder

    with pytest.raises(DeadlineExceededError) as excinfo:
        client.download("https://example.com/file.bin", tmp_path / "out.bin")

    assert isinstance(excinfo.value.__cause__, grpc.RpcError)


def test_cancelled_error_is_translated(
    client: Client,
    fake_daemon: FakeDfdaemon,
    tmp_path: Path,
) -> None:
    fake_daemon.responder = lambda request: (_ for _ in ()).throw(
        StreamAbort(grpc.StatusCode.CANCELLED, "cancelled")
    )

    with pytest.raises(DownloadCancelledError):
        client.download("https://example.com/file.bin", tmp_path / "out.bin")


def test_internal_error_is_translated(
    client: Client,
    fake_daemon: FakeDfdaemon,
    tmp_path: Path,
) -> None:
    fake_daemon.responder = lambda request: (_ for _ in ()).throw(
        StreamAbort(grpc.StatusCode.INTERNAL, "dfdaemon broke")
    )

    with pytest.raises(DfdaemonError, match="dfdaemon broke") as excinfo:
        client.download("https://example.com/file.bin", tmp_path / "out.bin")

    assert isinstance(excinfo.value.__cause__, grpc.RpcError)
    assert excinfo.value.code == "INTERNAL"


def test_partial_stream_fails(
    client: Client,
    fake_daemon: FakeDfdaemon,
    tmp_path: Path,
) -> None:
    from dragonfly_sdk import IncompleteDownloadError

    fake_daemon.responder = lambda request: [
        response
        for response in full_download(pieces=[(0, 5), (1, 5)])
        if not (
            response.WhichOneof("response") == "download_piece_finished_response"
            and response.download_piece_finished_response.piece.number == 1
        )
    ]

    with pytest.raises(IncompleteDownloadError) as excinfo:
        client.download("https://example.com/file.bin", tmp_path / "out.bin")

    assert excinfo.value.missing_pieces == (1,)


def test_channel_options_defaults_and_overrides() -> None:
    defaults = dict(_DEFAULT_CHANNEL_OPTIONS)
    assert defaults["grpc.max_receive_message_length"] == 256 * 1024 * 1024
    assert defaults["grpc.max_send_message_length"] == 256 * 1024 * 1024

    merged = dict(Client._merge_channel_options([("grpc.keepalive_time_ms", 1000)]))

    assert merged["grpc.keepalive_time_ms"] == 1000
    assert merged["grpc.max_receive_message_length"] == 256 * 1024 * 1024


def test_invalid_channel_options() -> None:
    with pytest.raises(ValidationError):
        Client._merge_channel_options("nope")  # type: ignore[arg-type]

    with pytest.raises(ValidationError):
        Client._merge_channel_options([("only-name",)])  # type: ignore[list-item]

    with pytest.raises(ValidationError):
        Client._merge_channel_options([(1, "value")])  # type: ignore[list-item]
