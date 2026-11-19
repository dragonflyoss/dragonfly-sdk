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

"""Shared helpers for the test suite."""

from __future__ import annotations

from collections.abc import Iterator, Sequence
from pathlib import Path
from typing import Any

import grpc
from dragonfly_api import common_pb2, dfdaemon_pb2, dfdaemon_pb2_grpc

from dragonfly_sdk._builder import build_download_task_request

PieceSpec = tuple[int, int]  # (number, length)


def build_request(output: Path, **overrides: Any) -> Any:
    """Build a request with dfget defaults and the given overrides."""
    kwargs: dict[str, Any] = {
        "url": "https://example.com/file.bin",
        "output": output,
        "timeout_seconds": 60.0,
    }
    kwargs.update(overrides)
    return build_download_task_request(**kwargs)


class StreamAbort(Exception):
    """Raised by a fake responder to abort the gRPC stream."""

    def __init__(self, code: grpc.StatusCode, message: str) -> None:
        super().__init__(message)
        self.code = code
        self.message = message


def started_response(
    *,
    pieces: Sequence[PieceSpec] = ((0, 5), (1, 5)),
    content_length: int = 10,
    task_id: str = "task-1",
    peer_id: str = "peer-1",
    host_id: str = "host-1",
    is_finished: bool = False,
    response_header: dict[str, str] | None = None,
) -> dfdaemon_pb2.DownloadTaskResponse:
    """Build a ``DownloadTaskStartedResponse`` wrapped in its task response."""
    offset = 0
    pb_pieces = []
    for number, length in pieces:
        pb_pieces.append(common_pb2.Piece(number=number, offset=offset, length=length))
        offset += length

    return dfdaemon_pb2.DownloadTaskResponse(
        task_id=task_id,
        peer_id=peer_id,
        host_id=host_id,
        download_task_started_response=dfdaemon_pb2.DownloadTaskStartedResponse(
            content_length=content_length,
            response_header=response_header or {},
            pieces=pb_pieces,
            is_finished=is_finished,
        ),
    )


def piece_response(
    number: int,
    length: int,
    *,
    task_id: str = "task-1",
    peer_id: str = "peer-1",
    host_id: str = "host-1",
) -> dfdaemon_pb2.DownloadTaskResponse:
    """Build a ``DownloadPieceFinishedResponse`` wrapped in its task response."""
    return dfdaemon_pb2.DownloadTaskResponse(
        task_id=task_id,
        peer_id=peer_id,
        host_id=host_id,
        download_piece_finished_response=dfdaemon_pb2.DownloadPieceFinishedResponse(
            piece=common_pb2.Piece(number=number, length=length)
        ),
    )


def full_download(
    pieces: Sequence[PieceSpec] = ((0, 5), (1, 5)),
    **started_kwargs: Any,
) -> list[dfdaemon_pb2.DownloadTaskResponse]:
    """Build a complete stream: one started response and one response per piece."""
    responses = [started_response(pieces=pieces, **started_kwargs)]
    responses.extend(piece_response(number, length) for number, length in pieces)
    return responses


class FakeDfdaemon(dfdaemon_pb2_grpc.DfdaemonDownloadServicer):
    """An in-process dfdaemon download service."""

    def __init__(self) -> None:
        self.requests: list[dfdaemon_pb2.DownloadTaskRequest] = []
        self.responder: Any = lambda request: full_download()
        self.socket_path: Path | None = None

    def DownloadTask(
        self,
        request: dfdaemon_pb2.DownloadTaskRequest,
        context: Any,
    ) -> Iterator[dfdaemon_pb2.DownloadTaskResponse]:
        self.requests.append(request)
        try:
            yield from self.responder(request)
        except StreamAbort as exc:
            context.abort(exc.code, exc.message)
