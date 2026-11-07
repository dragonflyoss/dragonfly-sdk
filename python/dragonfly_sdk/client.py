"""Client for dfdaemon's download gRPC service.

This is the Python equivalent of what the ``dfget`` CLI does: it dials the
dfdaemon download service and drives the server-streaming ``DownloadTask`` RPC.
It depends on the generated gRPC bindings published as the ``dragonfly-api``
package (https://pypi.org/project/dragonfly-api/).

Reference: dragonflyoss/client ``dragonfly-client/src/bin/dfget/main.rs``.
"""

from __future__ import annotations

import datetime
from dataclasses import dataclass
from typing import Iterable, Mapping, Optional

import grpc
from dragonfly_api import common_pb2, dfdaemon_pb2, dfdaemon_pb2_grpc

from .errors import DownloadError

DEFAULT_DFDAEMON_SOCKET_PATH = "/var/run/dragonfly/dfdaemon.sock"

DEFAULT_PRIORITY = common_pb2.LEVEL6


@dataclass
class DownloadResult:
    """Outcome of a completed download task."""

    task_id: str
    peer_id: str
    host_id: str
    output_path: str
    content_length: Optional[int] = None


def build_download(
    url: str,
    output_path: str,
    *,
    tag: Optional[str] = None,
    application: Optional[str] = None,
    priority: int = DEFAULT_PRIORITY,
    digest: Optional[str] = None,
    piece_length: Optional[int] = None,
    filtered_query_params: Optional[Iterable[str]] = None,
    request_header: Optional[Mapping[str, str]] = None,
    timeout: Optional[datetime.timedelta] = None,
    disable_back_to_source: bool = False,
    force_hard_link: bool = False,
    overwrite: bool = False,
) -> common_pb2.Download:
    """Build a ``common.v2.Download`` message mirroring ``dfget``'s defaults."""
    download = common_pb2.Download(
        url=url,
        type=common_pb2.STANDARD,
        output_path=output_path,
        priority=priority,
        disable_back_to_source=disable_back_to_source,
        force_hard_link=force_hard_link,
        overwrite=overwrite,
    )
    if tag is not None:
        download.tag = tag
    if application is not None:
        download.application = application
    if digest is not None:
        download.digest = digest
    if piece_length is not None:
        download.piece_length = piece_length
    if filtered_query_params:
        download.filtered_query_params.extend(filtered_query_params)
    if request_header:
        download.request_header.update(request_header)
    if timeout is not None:
        download.timeout.FromTimedelta(timeout)
    return download


class Client:
    """Client for dfdaemon's download gRPC service.
    """

    def __init__(self, socket_path: str = DEFAULT_DFDAEMON_SOCKET_PATH):
        self.socket_path = socket_path
        self._channel = grpc.insecure_channel(f"unix:{socket_path}")
        self._stub = dfdaemon_pb2_grpc.DfdaemonDownloadStub(self._channel)

    def __enter__(self) -> "Client":
        return self

    def __exit__(self, *exc) -> None:
        self.close()

    def close(self) -> None:
        self._channel.close()

    def download(self, url: str, output_path: str, **options) -> DownloadResult:
        """Download ``url`` to ``output_path`` via dfdaemon."""
        download = build_download(url, output_path, **options)
        request = dfdaemon_pb2.DownloadTaskRequest(download=download)

        host_id = task_id = peer_id = ""
        content_length: Optional[int] = None
        try:
            for response in self._stub.DownloadTask(request):
                host_id = response.host_id or host_id
                task_id = response.task_id or task_id
                peer_id = response.peer_id or peer_id
                if response.HasField("download_task_started_response"):
                    content_length = (
                        response.download_task_started_response.content_length
                    )
        except grpc.RpcError as err:
            raise DownloadError(f"failed to download {url}: {err}") from err

        return DownloadResult(
            task_id=task_id,
            peer_id=peer_id,
            host_id=host_id,
            output_path=output_path,
            content_length=content_length,
        )
