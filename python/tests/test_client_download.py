"""Integration tests for Client.download against an in-process fake dfdaemon.
"""

import contextlib
import os
import shutil
import tempfile
from concurrent import futures

import grpc
import pytest
from dragonfly_api import dfdaemon_pb2, dfdaemon_pb2_grpc

from dragonfly_sdk import Client, DownloadError


class _FakeDownloadServicer(dfdaemon_pb2_grpc.DfdaemonDownloadServicer):
    """Streams a started + piece-finished response, or aborts with an error."""

    def __init__(self, content_length=1024, error=None):
        self._content_length = content_length
        self._error = error

    def DownloadTask(self, request, context):
        if self._error is not None:
            context.abort(self._error, "download failed")

        yield dfdaemon_pb2.DownloadTaskResponse(
            host_id="host-1",
            task_id="task-1",
            peer_id="peer-1",
            download_task_started_response=dfdaemon_pb2.DownloadTaskStartedResponse(
                content_length=self._content_length,
            ),
        )
        yield dfdaemon_pb2.DownloadTaskResponse(
            host_id="host-1",
            task_id="task-1",
            peer_id="peer-1",
            download_piece_finished_response=dfdaemon_pb2.DownloadPieceFinishedResponse(),
        )


@contextlib.contextmanager
def _serve(servicer):
    # Keep the socket path short: macOS caps Unix socket paths at ~104 bytes,
    # and the default temp dir there can be long enough to overflow it.
    socket_dir = tempfile.mkdtemp(dir="/tmp")
    socket_path = os.path.join(socket_dir, "dfdaemon.sock")
    server = grpc.server(futures.ThreadPoolExecutor(max_workers=1))
    dfdaemon_pb2_grpc.add_DfdaemonDownloadServicer_to_server(servicer, server)
    server.add_insecure_port(f"unix:{socket_path}")
    server.start()
    try:
        yield socket_path
    finally:
        server.stop(0)
        shutil.rmtree(socket_dir, ignore_errors=True)


def test_download_drains_stream_and_returns_result():
    with _serve(_FakeDownloadServicer(content_length=2048)) as socket_path:
        with Client(socket_path) as client:
            result = client.download("https://example.com/file", "/tmp/out")

    assert result.task_id == "task-1"
    assert result.peer_id == "peer-1"
    assert result.host_id == "host-1"
    assert result.content_length == 2048
    assert result.output_path == "/tmp/out"


def test_download_wraps_rpc_error():
    servicer = _FakeDownloadServicer(error=grpc.StatusCode.NOT_FOUND)
    with _serve(servicer) as socket_path:
        with Client(socket_path) as client:
            with pytest.raises(DownloadError):
                client.download("https://example.com/missing", "/tmp/out")
