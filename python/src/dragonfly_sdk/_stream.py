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

"""Consumption of the dfdaemon ``DownloadTask`` server stream."""

from __future__ import annotations

import time
from collections.abc import Iterable
from datetime import timedelta
from pathlib import Path
from types import MappingProxyType
from typing import Any

from ._errors import IncompleteDownloadError
from ._progress import DownloadProgress, ProgressCallback
from ._result import DownloadResult


def consume_download_stream(
    responses: Iterable[Any],
    *,
    url: str,
    output_path: Path,
    on_progress: ProgressCallback | None = None,
) -> DownloadResult:
    """Consume a ``DownloadTask`` stream and summarize the download.

    A download succeeds when the stream ends without a gRPC error and every
    piece announced in the started response was reported as finished. dfdaemon
    does not send a terminal message: a clean end of stream means completion.

    The output file is never created, truncated or removed here; dfdaemon owns
    the file on disk. A failed download may therefore leave a partial file
    behind.
    """
    started_at = time.monotonic()

    started = False
    content_length = 0
    total_pieces = 0
    expected: dict[int, int] = {}
    seen: dict[int, int] = {}
    response_headers: dict[str, str] = {}
    downloaded_bytes = 0

    task_id = ""
    peer_id = ""
    host_id = ""

    def elapsed() -> timedelta:
        return timedelta(seconds=time.monotonic() - started_at)

    def emit(piece_number: int | None) -> None:
        if on_progress is None:
            return

        completed_pieces = len(seen)
        percent = 100.0 if total_pieces == 0 else completed_pieces * 100.0 / total_pieces

        duration = time.monotonic() - started_at
        speed = downloaded_bytes / duration if duration > 0 else 0.0

        on_progress(
            DownloadProgress(
                url=url,
                output_path=output_path,
                content_length=content_length,
                downloaded_bytes=downloaded_bytes,
                total_pieces=total_pieces,
                completed_pieces=completed_pieces,
                piece_number=piece_number,
                percent=percent,
                elapsed=timedelta(seconds=duration),
                speed_bytes_per_second=speed,
            )
        )

    for response in responses:
        if not task_id and response.task_id:
            task_id = response.task_id
        if not peer_id and response.peer_id:
            peer_id = response.peer_id
        if not host_id and response.host_id:
            host_id = response.host_id

        kind = response.WhichOneof("response")

        if kind == "download_task_started_response":
            if started:
                # Only the first started response carries the task metadata.
                continue

            started = True
            started_response = response.download_task_started_response
            content_length = started_response.content_length
            response_headers = dict(started_response.response_header)
            expected = {piece.number: piece.length for piece in started_response.pieces}
            total_pieces = len(expected)
            emit(None)

        elif kind == "download_piece_finished_response":
            if not started:
                continue

            piece_response = response.download_piece_finished_response
            if not piece_response.HasField("piece"):
                continue

            piece = piece_response.piece
            number = piece.number
            if number in seen or number not in expected:
                continue

            seen[number] = piece.length
            downloaded_bytes += piece.length
            emit(number)

    if not started:
        raise IncompleteDownloadError(
            "dfdaemon closed the download stream without starting the task",
            missing_pieces=(),
            total_pieces=0,
        )

    missing = sorted(set(expected) - set(seen))
    if missing:
        raise IncompleteDownloadError(
            f"download finished with {len(missing)} of {total_pieces} pieces missing",
            missing_pieces=missing,
            total_pieces=total_pieces,
        )

    return DownloadResult(
        url=url,
        output_path=output_path,
        task_id=task_id,
        peer_id=peer_id,
        host_id=host_id,
        content_length=content_length,
        total_pieces=total_pieces,
        completed_pieces=len(seen),
        downloaded_bytes=downloaded_bytes,
        response_headers=MappingProxyType(response_headers),
        elapsed=elapsed(),
    )
