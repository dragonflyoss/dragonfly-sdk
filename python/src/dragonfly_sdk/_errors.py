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

"""Exceptions raised by dragonfly-sdk and decoding of dfdaemon gRPC errors."""

from __future__ import annotations

import json
from collections.abc import Iterable, Mapping, Sequence
from typing import Any, cast

import grpc

# dfdaemon reports backend errors by attaching a JSON payload to the gRPC status
# details. grpcio exposes the `-bin` trailing metadata already base64-decoded, so
# the value is the raw JSON bytes.
_STATUS_DETAILS_KEY = "grpc-status-details-bin"


class DragonflyError(Exception):
    """Base class for all errors raised by dragonfly-sdk."""


class ValidationError(DragonflyError, ValueError):
    """The arguments passed to the SDK are invalid."""


class ConnectionFailedError(DragonflyError):
    """The SDK could not connect to the local dfdaemon."""


class ClientClosedError(DragonflyError):
    """The client has been closed and can no longer be used."""


class DownloadFailedError(DragonflyError):
    """A download started by the SDK failed."""


class DfdaemonError(DownloadFailedError):
    """dfdaemon returned an error that is not a backend (HTTP) error."""

    def __init__(
        self,
        message: str,
        *,
        code: str | None = None,
        details: str | None = None,
    ) -> None:
        super().__init__(message)
        self.code = code
        self.details = details


class BackendError(DownloadFailedError):
    """The upstream source returned an error response."""

    def __init__(
        self,
        message: str,
        *,
        headers: Mapping[str, str] | None = None,
        status_code: int | None = None,
    ) -> None:
        super().__init__(message)
        self.headers = dict(headers or {})
        self.status_code = status_code


class IncompleteDownloadError(DownloadFailedError):
    """dfdaemon closed the stream before every piece was delivered."""

    def __init__(
        self,
        message: str,
        *,
        missing_pieces: Sequence[int] = (),
        total_pieces: int = 0,
    ) -> None:
        super().__init__(message)
        self.missing_pieces = tuple(missing_pieces)
        self.total_pieces = total_pieces


class DeadlineExceededError(DownloadFailedError, TimeoutError):
    """The download exceeded the configured timeout."""


class DownloadCancelledError(DownloadFailedError):
    """The download was cancelled by dfdaemon or by closing the client."""


def translate_rpc_error(exc: grpc.RpcError) -> DownloadFailedError:
    """Map a gRPC error raised by the download stream to a typed SDK error."""
    code = exc.code()
    details = exc.details()

    if code == grpc.StatusCode.DEADLINE_EXCEEDED:
        return DeadlineExceededError(details or "download deadline exceeded")

    if code == grpc.StatusCode.CANCELLED:
        return DownloadCancelledError(details or "download cancelled")

    backend_error = _decode_backend_error(exc)
    if backend_error is not None:
        return backend_error

    message = details or code.name
    return DfdaemonError(message, code=code.name, details=details or None)


def _decode_backend_error(exc: grpc.RpcError) -> BackendError | None:
    """Decode the JSON status details dfdaemon attaches for backend errors."""
    try:
        # grpcio returns named tuples here, which the stubs do not describe as
        # iterable even though they are.
        metadata = cast(Iterable[tuple[str, Any]], exc.trailing_metadata() or ())
        raw: Any = dict(metadata).get(_STATUS_DETAILS_KEY)
    except Exception:  # pragma: no cover - defensive against fake RpcError objects
        return None

    if raw is None:
        return None

    try:
        payload: Any = json.loads(raw)
    except (TypeError, ValueError):
        return None

    if not isinstance(payload, dict):
        return None

    message = payload.get("message")
    if not isinstance(message, str):
        return None

    header = payload.get("header") or {}
    if not isinstance(header, dict):
        return None

    headers: dict[str, str] = {}
    for key, value in header.items():
        if not isinstance(key, str) or not isinstance(value, str):
            return None
        headers[key] = value

    status_code = payload.get("status_code")
    if status_code is not None and (
        isinstance(status_code, bool) or not isinstance(status_code, int)
    ):
        return None

    return BackendError(message, headers=headers, status_code=status_code)
