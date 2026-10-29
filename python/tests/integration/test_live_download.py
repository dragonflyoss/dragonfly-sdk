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

"""End-to-end test against a live dfdaemon.

Run with::

    DRAGONFLY_LIVE_TEST=1 pytest -q -m integration

Set ``DRAGONFLY_DFDAEMON_SOCKET`` to use a non-default socket path.
"""

from __future__ import annotations

import os
import threading
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

from dragonfly_sdk import Client, DownloadProgress

pytestmark = [
    pytest.mark.integration,
    pytest.mark.skipif(
        os.environ.get("DRAGONFLY_LIVE_TEST") != "1",
        reason="set DRAGONFLY_LIVE_TEST=1 to run against a live dfdaemon",
    ),
]

PAYLOAD = bytes(range(256)) * 4096  # 1 MiB, easy to compare byte by byte


class PayloadHandler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        self.send_response(200)
        self.send_header("Content-Length", str(len(PAYLOAD)))
        self.send_header("Content-Type", "application/octet-stream")
        self.end_headers()
        self.wfile.write(PAYLOAD)

    def log_message(self, *args: object) -> None:
        pass


@pytest.fixture
def payload_url() -> Iterator[str]:
    server = ThreadingHTTPServer(("127.0.0.1", 0), PayloadHandler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    try:
        yield f"http://127.0.0.1:{server.server_address[1]}/payload.bin"
    finally:
        server.shutdown()
        server.server_close()
        thread.join()


def test_live_download(payload_url: str, tmp_path: Path) -> None:
    socket = os.environ.get("DRAGONFLY_DFDAEMON_SOCKET")
    output = tmp_path / "payload.bin"
    events: list[DownloadProgress] = []

    with Client(endpoint=socket) as client:
        result = client.download(
            payload_url,
            output,
            timeout=600,
            on_progress=events.append,
        )

    assert output.read_bytes() == PAYLOAD
    assert result.content_length == len(PAYLOAD)
    assert result.total_pieces > 0
    assert result.completed_pieces == result.total_pieces
    assert result.downloaded_bytes == len(PAYLOAD)
    assert events
    assert events[-1].percent == 100.0
    assert events[-1].completed_pieces == events[-1].total_pieces
