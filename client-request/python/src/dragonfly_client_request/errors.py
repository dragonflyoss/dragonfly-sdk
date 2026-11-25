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

from __future__ import annotations

from http import HTTPStatus

__all__ = [
    "BackendError",
    "DfdaemonError",
    "InternalError",
    "InvalidArgumentError",
    "ProxyError",
    "RequestError",
    "RequestTimeoutError",
    "ResponseError",
]


class RequestError(Exception):
    """The base error of the request library."""


class RequestTimeoutError(RequestError):
    """Indicates the request timed out."""


class InvalidArgumentError(RequestError):
    """Indicates an invalid argument."""


class InternalError(RequestError):
    """Indicates a request internal error."""


def _is_retryable_status(status: int) -> bool:
    """Reports whether the status of a proxy, backend or dfdaemon answer marks
    a transient failure: 5xx, 408 or 429.
    """
    return (
        status >= HTTPStatus.INTERNAL_SERVER_ERROR
        or status == HTTPStatus.REQUEST_TIMEOUT
        or status == HTTPStatus.TOO_MANY_REQUESTS
    )


class ResponseError(RequestError):
    """The base error of an answer from a server in the request path."""

    # server names the server the answer came from in the error message.
    server = "server"

    def __init__(self, message: str, headers: dict[str, str], status_code: int) -> None:
        super().__init__(message)
        self.message = message
        self.headers = headers
        self.status_code = status_code

    def __str__(self) -> str:
        return (
            f"{self.server} error, message: {self.message!r}, "
            f"header: {self.headers}, status_code: {self.status_code}"
        )

    def retryable(self) -> bool:
        """Reports whether the answer marks a transient failure."""
        return _is_retryable_status(self.status_code)


class BackendError(ResponseError):
    """The error detail returned by the backend server."""

    server = "backend server"


class ProxyError(ResponseError):
    """The error detail returned by the proxy server."""

    server = "proxy server"


class DfdaemonError(ResponseError):
    """The error detail returned by the dfdaemon when the seed peer could not
    run the download task.

    | Status | Meaning                                                          |
    |--------|------------------------------------------------------------------|
    | 400    | invalid request or URL                                           |
    | 422    | the origin sent no Content-Length, or the piece length is invalid |
    | 507    | the seed peer has no room for the task                           |
    | 500    | any other failure: scheduling, peers, streaming                  |
    """

    server = "dfdaemon"


def is_retryable(err: BaseException) -> bool:
    """Reports whether a failed attempt is worth another one against a
    different seed peer.

    | Failure                                                  | Retry |
    |----------------------------------------------------------|-------|
    | RequestTimeoutError, InternalError (connect, transport)  | yes   |
    | proxy / backend / dfdaemon answer 5xx, 408 or 429        | yes   |
    | any other answer, such as 403, 404, 422                  | no    |
    | InvalidArgumentError                                     | no    |

    429 and 507 are retried because another seed peer may not be rate limited
    or out of space. Any other 4xx is definitive: every seed peer would answer
    the same.
    """
    if isinstance(err, ResponseError):
        return err.retryable()

    return isinstance(err, (RequestTimeoutError, InternalError))
