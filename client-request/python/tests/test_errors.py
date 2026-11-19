#     Copyright 2026 The Dragonfly Authors
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

# Mirrors the HTTP status cases of the Go SDK's errors_test.go TestIsRetryable
# and the Rust crate's errors.rs is_retryable_marks_only_transient_failures.
# The gRPC status cases are not mirrored until the scheduler client exists.

import pytest

from dragonfly_client_request.errors import (
    BackendError,
    DfdaemonError,
    InternalError,
    InvalidArgumentError,
    ProxyError,
    RequestError,
    RequestTimeoutError,
    ResponseError,
    is_retryable,
)


@pytest.mark.parametrize(
    ("err", "expected"),
    [
        (RequestTimeoutError("deadline"), True),
        (InternalError("boom"), True),
        (DfdaemonError("", {}, 500), True),
        (DfdaemonError("", {}, 507), True),
        (DfdaemonError("", {}, 422), False),
        (DfdaemonError("", {}, 400), False),
        (BackendError("", {}, 500), True),
        (BackendError("", {}, 502), True),
        (BackendError("", {}, 503), True),
        (BackendError("", {}, 408), True),
        (BackendError("", {}, 429), True),
        (ProxyError("", {}, 500), True),
        (ProxyError("", {}, 502), True),
        (ProxyError("", {}, 503), True),
        (ProxyError("", {}, 408), True),
        (ProxyError("", {}, 429), True),
        (BackendError("", {}, 400), False),
        (BackendError("", {}, 401), False),
        (BackendError("", {}, 403), False),
        (BackendError("", {}, 404), False),
        (BackendError("", {}, 416), False),
        (ProxyError("", {}, 400), False),
        (ProxyError("", {}, 401), False),
        (ProxyError("", {}, 403), False),
        (ProxyError("", {}, 404), False),
        (ProxyError("", {}, 416), False),
        (InvalidArgumentError("bad"), False),
        (RequestError("base"), False),
        (ValueError("foreign"), False),
    ],
)
def test_is_retryable_marks_only_transient_failures(err: BaseException, expected: bool) -> None:
    assert is_retryable(err) == expected


@pytest.mark.parametrize(
    ("err", "expected"),
    [
        (
            ProxyError("boom", {"a": "b"}, 503),
            "proxy server error, message: 'boom', header: {'a': 'b'}, status_code: 503",
        ),
        (
            BackendError("boom", {}, 404),
            "backend server error, message: 'boom', header: {}, status_code: 404",
        ),
        (
            DfdaemonError("boom", {}, 500),
            "dfdaemon error, message: 'boom', header: {}, status_code: 500",
        ),
    ],
)
def test_response_error_str(err: ResponseError, expected: str) -> None:
    assert str(err) == expected
    assert isinstance(err, RequestError)
    assert err.message == "boom"


def test_error_hierarchy() -> None:
    for cls in (RequestTimeoutError, InvalidArgumentError, InternalError, ResponseError):
        assert issubclass(cls, RequestError)
    for cls in (BackendError, ProxyError, DfdaemonError):
        assert issubclass(cls, ResponseError)
