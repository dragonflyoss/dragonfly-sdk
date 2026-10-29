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

"""Synchronous client for downloading files through a local dfdaemon."""

from __future__ import annotations

import math
import threading
from collections.abc import Sequence
from pathlib import Path
from typing import Any

import grpc
from dragonfly_api import dfdaemon_pb2_grpc

from ._builder import (
    DEFAULT_PRIORITY,
    DEFAULT_TIMEOUT,
    OutputLike,
    SchedulingPolicyLike,
    TimeoutLike,
    build_download_task_request,
    timeout_to_seconds,
)
from ._credentials import Credentials
from ._endpoint import EndpointLike, channel_target, resolve_endpoint
from ._errors import (
    ClientClosedError,
    ConnectionFailedError,
    ValidationError,
    translate_rpc_error,
)
from ._headers import HeadersLike
from ._progress import ProgressCallback
from ._result import DownloadResult
from ._stream import consume_download_stream

DEFAULT_CONNECT_TIMEOUT = 3.0

# The started response carries one Piece per piece of the file, so the default
# 4 MiB gRPC message limit is too small for large files.
_DEFAULT_CHANNEL_OPTIONS: tuple[tuple[str, Any], ...] = (
    ("grpc.max_receive_message_length", 256 * 1024 * 1024),
    ("grpc.max_send_message_length", 256 * 1024 * 1024),
    ("grpc.keepalive_time_ms", 30_000),
    ("grpc.keepalive_timeout_ms", 10_000),
    ("grpc.keepalive_permit_without_calls", 1),
    ("grpc.enable_retries", 0),
)


class Client:
    """A synchronous client for a local dfdaemon download service.

    The client talks to dfdaemon over its Unix domain socket. It never
    downloads data itself: dfdaemon fetches the file (using P2P where possible)
    and writes it to the requested output path.

    The client is thread-safe: a single instance may run several downloads
    concurrently. Closing the client cancels in-flight downloads.

    Example:
        >>> with Client() as client:
        ...     result = client.download("https://example.com/model.bin", "./model.bin")
        ...     print(result.content_length)
    """

    def __init__(
        self,
        endpoint: EndpointLike | None = None,
        *,
        connect_timeout: float = DEFAULT_CONNECT_TIMEOUT,
        channel_options: Sequence[tuple[str, Any]] | None = None,
    ) -> None:
        """Connect to dfdaemon.

        Args:
            endpoint: Path to the dfdaemon Unix socket. Defaults to the socket
                path dfdaemon uses on the current OS.
            connect_timeout: Seconds to wait for the socket to become ready.
            channel_options: Extra ``(name, value)`` gRPC channel options.
                They override the SDK defaults with the same name.
        """
        if (
            isinstance(connect_timeout, bool)
            or not isinstance(connect_timeout, (int, float))
            or not math.isfinite(float(connect_timeout))
            or connect_timeout <= 0
        ):
            raise ValidationError("connect_timeout must be a positive number of seconds")

        self._endpoint = resolve_endpoint(endpoint)

        if not self._endpoint.exists():
            raise ConnectionFailedError(
                f"dfdaemon socket not found at {self._endpoint}; is dfdaemon running?"
            )

        options = self._merge_channel_options(channel_options)
        self._channel = grpc.insecure_channel(channel_target(self._endpoint), options=options)

        try:
            grpc.channel_ready_future(self._channel).result(timeout=connect_timeout)
        except grpc.FutureTimeoutError as exc:
            self._channel.close()
            raise ConnectionFailedError(
                f"timed out after {connect_timeout:g}s while connecting to dfdaemon "
                f"at {self._endpoint}"
            ) from exc
        except Exception as exc:  # pragma: no cover - defensive
            self._channel.close()
            raise ConnectionFailedError(
                f"failed to connect to dfdaemon at {self._endpoint}"
            ) from exc

        self._stub = dfdaemon_pb2_grpc.DfdaemonDownloadStub(self._channel)
        self._closed = False
        self._close_lock = threading.Lock()

    @property
    def endpoint(self) -> Path:
        """The dfdaemon socket path this client is connected to."""
        return self._endpoint

    @property
    def closed(self) -> bool:
        """Whether :meth:`close` has been called."""
        return self._closed

    def download(
        self,
        url: str,
        output: OutputLike,
        *,
        headers: HeadersLike = None,
        digest: str | None = None,
        tag: str = "",
        application: str = "",
        priority: int = DEFAULT_PRIORITY,
        timeout: TimeoutLike = DEFAULT_TIMEOUT,
        piece_length: int | None = None,
        filtered_query_params: Sequence[str] | None = None,
        overwrite: bool = False,
        force_hard_link: bool = False,
        disable_back_to_source: bool = False,
        scheduling_policy: SchedulingPolicyLike = "auto",
        content_for_calculating_task_id: str | None = None,
        credentials: Credentials | None = None,
        on_progress: ProgressCallback | None = None,
    ) -> DownloadResult:
        """Download a single file through dfdaemon.

        This call blocks until the download finishes, fails, or the timeout is
        reached. The output file is written by dfdaemon; if the download fails
        a partial file may remain on disk.

        Args:
            url: Absolute URL of the file. Supported schemes are ``http``,
                ``https``, ``s3``, ``gs``, ``abs``, ``oss``, ``obs``, ``cos``,
                ``hf``, ``modelscope`` and ``opencsg``.
            output: Path of the file dfdaemon writes.
            headers: Request headers, either a mapping or ``"Name: value"``
                strings.
            digest: Expected digest, in ``<algorithm>:<hex>`` form.
            tag: dfdaemon task tag.
            application: dfdaemon application name.
            priority: Scheduling priority from 0 to 6.
            timeout: Download timeout as seconds or a ``timedelta``.
            piece_length: Piece length in bytes, at least 4 MiB.
            filtered_query_params: Query parameters excluded from task-id
                generation. Replaces the dfget default list when given.
            overwrite: Replace an existing output file.
            force_hard_link: Fail unless dfdaemon can hard-link the output.
            disable_back_to_source: Do not fall back to the source origin.
            scheduling_policy: ``"auto"``/``"always"`` (or ``0``/``1``).
            content_for_calculating_task_id: Extra content mixed into the task
                id calculation.
            credentials: Backend credentials matching the URL scheme.
            on_progress: Called on the calling thread with progress snapshots.

        Returns:
            A :class:`DownloadResult` describing the completed download.

        Raises:
            ValidationError: The arguments are invalid.
            ClientClosedError: The client was closed.
            ConnectionFailedError: The connection to dfdaemon failed.
            DownloadFailedError: The download failed; see its subclasses.
        """
        if self._closed:
            raise ClientClosedError("client is closed")

        timeout_seconds = timeout_to_seconds(timeout)
        request = build_download_task_request(
            url=url,
            output=output,
            headers=headers,
            digest=digest,
            tag=tag,
            application=application,
            priority=priority,
            timeout_seconds=timeout_seconds,
            piece_length=piece_length,
            filtered_query_params=filtered_query_params,
            overwrite=overwrite,
            force_hard_link=force_hard_link,
            disable_back_to_source=disable_back_to_source,
            scheduling_policy=scheduling_policy,
            content_for_calculating_task_id=content_for_calculating_task_id,
            credentials=credentials,
        )

        output_path = Path(request.download.output_path)

        try:
            call = self._stub.DownloadTask(request, timeout=timeout_seconds)
        except ValueError as exc:
            # grpc raises ValueError when the channel was closed concurrently.
            raise ClientClosedError("client was closed before the download started") from exc

        try:
            return consume_download_stream(
                call,
                url=url,
                output_path=output_path,
                on_progress=on_progress,
            )
        except grpc.RpcError as exc:
            raise translate_rpc_error(exc) from exc
        finally:
            if not call.done():
                call.cancel()

    def close(self) -> None:
        """Close the connection to dfdaemon and cancel in-flight downloads."""
        with self._close_lock:
            if self._closed:
                return
            self._closed = True

        self._channel.close()

    def __enter__(self) -> Client:
        return self

    def __exit__(self, *exc_info: object) -> None:
        self.close()

    def __repr__(self) -> str:
        return f"Client(endpoint={str(self._endpoint)!r}, closed={self._closed!r})"

    @staticmethod
    def _merge_channel_options(
        channel_options: Sequence[tuple[str, Any]] | None,
    ) -> list[tuple[str, Any]]:
        merged = dict(_DEFAULT_CHANNEL_OPTIONS)
        if channel_options is not None:
            if isinstance(channel_options, (str, bytes)):
                raise ValidationError("channel_options must be a sequence of (name, value) pairs")

            for option in channel_options:
                if not isinstance(option, tuple) or len(option) != 2:
                    raise ValidationError(
                        "channel_options must be a sequence of (name, value) pairs"
                    )
                name, value = option
                if not isinstance(name, str):
                    raise ValidationError("channel option names must be strings")
                merged[name] = value

        return list(merged.items())
