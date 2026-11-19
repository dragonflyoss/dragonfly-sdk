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

from dragonfly_sdk import DownloadProgress, IncompleteDownloadError
from dragonfly_sdk._stream import consume_download_stream
from helpers import full_download, piece_response, started_response

OUTPUT = Path("/tmp/out.bin")
URL = "https://example.com/file.bin"


def consume(responses, on_progress=None):  # type: ignore[no-untyped-def]
    return consume_download_stream(responses, url=URL, output_path=OUTPUT, on_progress=on_progress)


def test_full_download_succeeds() -> None:
    result = consume(full_download(pieces=[(0, 5), (1, 5)]))

    assert result.url == URL
    assert result.output_path == OUTPUT
    assert result.task_id == "task-1"
    assert result.peer_id == "peer-1"
    assert result.host_id == "host-1"
    assert result.content_length == 10
    assert result.total_pieces == 2
    assert result.completed_pieces == 2
    assert result.downloaded_bytes == 10
    assert result.elapsed.total_seconds() >= 0


def test_response_headers_are_preserved() -> None:
    responses = full_download(
        pieces=[(0, 5), (1, 5)],
        response_header={"Content-Type": "application/octet-stream"},
    )
    result = consume(responses)

    assert result.response_headers == {"Content-Type": "application/octet-stream"}


def test_cached_task_succeeds_without_pieces() -> None:
    responses = [started_response(pieces=[], content_length=10, is_finished=True)]
    result = consume(responses)

    assert result.completed_pieces == 0
    assert result.total_pieces == 0
    assert result.content_length == 10


def test_empty_stream_is_incomplete() -> None:
    with pytest.raises(IncompleteDownloadError, match="without starting"):
        consume([])


def test_missing_pieces_are_reported() -> None:
    responses = [started_response(pieces=[(0, 5), (1, 5)]), piece_response(0, 5)]

    with pytest.raises(IncompleteDownloadError) as excinfo:
        consume(responses)

    assert excinfo.value.missing_pieces == (1,)
    assert excinfo.value.total_pieces == 2


def test_duplicate_piece_is_counted_once() -> None:
    responses = [
        started_response(pieces=[(0, 5), (1, 5)]),
        piece_response(0, 5),
        piece_response(0, 5),
        piece_response(1, 5),
    ]
    result = consume(responses)

    assert result.completed_pieces == 2
    assert result.downloaded_bytes == 10


def test_unknown_piece_is_ignored() -> None:
    responses = [
        started_response(pieces=[(0, 5)], content_length=5),
        piece_response(0, 5),
        piece_response(99, 5),
    ]
    result = consume(responses)

    assert result.completed_pieces == 1
    assert result.downloaded_bytes == 5


def test_piece_before_started_is_ignored() -> None:
    responses = [piece_response(0, 5), started_response(pieces=[], content_length=0)]
    result = consume(responses)

    assert result.completed_pieces == 0
    assert result.downloaded_bytes == 0


def test_piece_without_piece_payload_is_ignored() -> None:
    from dragonfly_api import dfdaemon_pb2

    responses = [
        started_response(pieces=[(0, 5)], content_length=5),
        dfdaemon_pb2.DownloadTaskResponse(
            download_piece_finished_response=dfdaemon_pb2.DownloadPieceFinishedResponse()
        ),
        piece_response(0, 5),
    ]
    result = consume(responses)

    assert result.completed_pieces == 1


def test_progress_is_monotonic_and_reaches_one_hundred() -> None:
    events: list[DownloadProgress] = []
    consume(full_download(pieces=[(0, 5), (1, 5)]), on_progress=events.append)

    assert [event.piece_number for event in events] == [None, 0, 1]
    assert [event.percent for event in events] == [0.0, 50.0, 100.0]
    assert [event.completed_pieces for event in events] == [0, 1, 2]
    assert [event.downloaded_bytes for event in events] == [0, 5, 10]
    assert all(event.speed_bytes_per_second >= 0 for event in events)
    assert all(event.total_pieces == 2 for event in events)


def test_progress_reaches_one_hundred_for_empty_task() -> None:
    events: list[DownloadProgress] = []
    consume([started_response(pieces=[], content_length=0)], on_progress=events.append)

    assert len(events) == 1
    assert events[0].percent == 100.0


def test_progress_callback_exception_propagates() -> None:
    def on_progress(progress: DownloadProgress) -> None:
        raise RuntimeError("callback failed")

    with pytest.raises(RuntimeError, match="callback failed"):
        consume(full_download(), on_progress=on_progress)
