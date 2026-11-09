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

import json
from collections.abc import Iterable
from typing import Any

import grpc

from dragonfly_sdk import (
    BackendError,
    DeadlineExceededError,
    DfdaemonError,
    DownloadCancelledError,
    DownloadFailedError,
)
from dragonfly_sdk._errors import translate_rpc_error

DETAILS_KEY = "grpc-status-details-bin"


class FakeRpcError(grpc.RpcError):
    def __init__(
        self,
        code: grpc.StatusCode = grpc.StatusCode.UNKNOWN,
        details: str = "boom",
        trailing: Iterable[tuple[str, Any]] = (),
    ) -> None:
        self._code = code
        self._details = details
        self._trailing = list(trailing)

    def code(self) -> grpc.StatusCode:
        return self._code

    def details(self) -> str:
        return self._details

    def trailing_metadata(self) -> list[tuple[str, Any]]:
        return self._trailing


def backend_error(details: bytes, *, details_text: str = "internal") -> FakeRpcError:
    return FakeRpcError(
        grpc.StatusCode.INTERNAL,
        details_text,
        trailing=[(DETAILS_KEY, details)],
    )


def payload(**fields: Any) -> bytes:
    return json.dumps(fields).encode()


def test_backend_error_is_decoded() -> None:
    error = translate_rpc_error(
        backend_error(
            payload(
                message="AccessDenied",
                header={"x-request-id": "abc"},
                status_code=403,
            )
        )
    )

    assert isinstance(error, BackendError)
    assert str(error) == "AccessDenied"
    assert error.status_code == 403
    assert error.headers == {"x-request-id": "abc"}


def test_backend_error_without_status_code() -> None:
    error = translate_rpc_error(backend_error(payload(message="not found")))

    assert isinstance(error, BackendError)
    assert error.status_code is None
    assert error.headers == {}


def test_backend_error_with_empty_header() -> None:
    error = translate_rpc_error(
        backend_error(payload(message="nope", header=None, status_code=404))
    )

    assert isinstance(error, BackendError)
    assert error.headers == {}


def test_non_json_details_fall_back_to_dfdaemon_error() -> None:
    error = translate_rpc_error(backend_error(b"not json"))

    assert isinstance(error, DfdaemonError)
    assert not isinstance(error, BackendError)
    assert error.code == "INTERNAL"
    assert error.details == "internal"


def test_non_object_json_falls_back() -> None:
    error = translate_rpc_error(backend_error(b'["a", "b"]'))

    assert isinstance(error, DfdaemonError)
    assert not isinstance(error, BackendError)


def test_missing_message_falls_back() -> None:
    error = translate_rpc_error(backend_error(payload(status_code=500)))

    assert isinstance(error, DfdaemonError)
    assert not isinstance(error, BackendError)


def test_non_string_message_falls_back() -> None:
    error = translate_rpc_error(backend_error(payload(message=42)))

    assert not isinstance(error, BackendError)


def test_non_string_header_value_falls_back() -> None:
    error = translate_rpc_error(backend_error(payload(message="x", header={"a": 1})))

    assert not isinstance(error, BackendError)


def test_boolean_status_code_falls_back() -> None:
    error = translate_rpc_error(backend_error(payload(message="x", status_code=True)))

    assert not isinstance(error, BackendError)


def test_missing_metadata_falls_back() -> None:
    error = translate_rpc_error(FakeRpcError(grpc.StatusCode.UNAVAILABLE, "unavailable"))

    assert isinstance(error, DfdaemonError)
    assert error.code == "UNAVAILABLE"
    assert error.details == "unavailable"
    assert str(error) == "unavailable"


def test_empty_details_use_status_name() -> None:
    error = translate_rpc_error(FakeRpcError(grpc.StatusCode.UNAVAILABLE, ""))

    assert isinstance(error, DfdaemonError)
    assert str(error) == "UNAVAILABLE"
    assert error.details is None


def test_deadline_exceeded_mapping() -> None:
    error = translate_rpc_error(FakeRpcError(grpc.StatusCode.DEADLINE_EXCEEDED, "too slow"))

    assert isinstance(error, DeadlineExceededError)
    assert isinstance(error, DownloadFailedError)
    assert isinstance(error, TimeoutError)
    assert str(error) == "too slow"


def test_cancelled_mapping() -> None:
    error = translate_rpc_error(FakeRpcError(grpc.StatusCode.CANCELLED, "cancelled"))

    assert isinstance(error, DownloadCancelledError)
    assert str(error) == "cancelled"


def test_cancelled_without_details() -> None:
    error = translate_rpc_error(FakeRpcError(grpc.StatusCode.CANCELLED, ""))

    assert str(error) == "download cancelled"


def test_exception_hierarchy() -> None:
    from dragonfly_sdk import DragonflyError, ValidationError

    assert issubclass(ValidationError, ValueError)
    assert issubclass(DeadlineExceededError, TimeoutError)
    for cls in (
        BackendError,
        DfdaemonError,
        DownloadCancelledError,
        DeadlineExceededError,
    ):
        assert issubclass(cls, DownloadFailedError)
        assert issubclass(cls, DragonflyError)
