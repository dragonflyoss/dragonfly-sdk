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

"""Shared pytest fixtures."""

from __future__ import annotations

import shutil
import tempfile
from collections.abc import Iterator
from concurrent import futures
from pathlib import Path

import grpc
import pytest
from dragonfly_api import dfdaemon_pb2_grpc

from dragonfly_sdk import Client
from helpers import FakeDfdaemon


@pytest.fixture
def fake_daemon() -> Iterator[FakeDfdaemon]:
    """Run a fake dfdaemon on a short Unix socket path.

    The socket lives directly under ``/tmp`` because macOS limits Unix socket
    paths to 104 bytes.
    """
    daemon = FakeDfdaemon()
    directory = Path(tempfile.mkdtemp(prefix="dfd", dir="/tmp"))
    socket_path = directory / "dfdaemon.sock"

    server = grpc.server(futures.ThreadPoolExecutor(max_workers=8))
    dfdaemon_pb2_grpc.add_DfdaemonDownloadServicer_to_server(daemon, server)
    assert server.add_insecure_port(f"unix:{socket_path}") != 0
    server.start()

    daemon.socket_path = socket_path

    try:
        yield daemon
    finally:
        server.stop(grace=0)
        shutil.rmtree(directory, ignore_errors=True)


@pytest.fixture
def client(fake_daemon: FakeDfdaemon) -> Iterator[Client]:
    """A client connected to the fake dfdaemon."""
    with Client(endpoint=fake_daemon.socket_path) as sdk_client:
        yield sdk_client
