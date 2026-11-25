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

"""Request library for the Dragonfly client. It is the Python implementation
of the Rust crate dragonfly-client-request and the Go module
d7y.io/dragonfly-sdk/client-request/go.
"""

from .errors import (
    BackendError,
    DfdaemonError,
    InternalError,
    InvalidArgumentError,
    ProxyError,
    RequestError,
    RequestTimeoutError,
    ResponseError,
)

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
