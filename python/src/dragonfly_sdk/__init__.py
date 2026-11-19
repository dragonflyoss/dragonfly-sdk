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

"""Python SDK for dfget.

Download files through a local dfdaemon with the same request semantics as the
``dfget`` CLI::

    from dragonfly_sdk import Client

    with Client() as client:
        result = client.download(
            "hf://Qwen/Qwen2.5-7B/model.safetensors",
            "./model.safetensors",
        )
"""

from __future__ import annotations

from ._client import Client
from ._credentials import (
    Credentials,
    HuggingFaceCredentials,
    ModelScopeCredentials,
    ObjectStorageCredentials,
    OpenCSGCredentials,
)
from ._errors import (
    BackendError,
    ClientClosedError,
    ConnectionFailedError,
    DeadlineExceededError,
    DfdaemonError,
    DownloadCancelledError,
    DownloadFailedError,
    DragonflyError,
    IncompleteDownloadError,
    ValidationError,
)
from ._progress import DownloadProgress, ProgressCallback
from ._result import DownloadResult
from ._version import __version__

__all__ = [
    "BackendError",
    "Client",
    "ClientClosedError",
    "ConnectionFailedError",
    "Credentials",
    "DeadlineExceededError",
    "DfdaemonError",
    "DownloadCancelledError",
    "DownloadFailedError",
    "DownloadProgress",
    "DownloadResult",
    "DragonflyError",
    "HuggingFaceCredentials",
    "IncompleteDownloadError",
    "ModelScopeCredentials",
    "ObjectStorageCredentials",
    "OpenCSGCredentials",
    "ProgressCallback",
    "ValidationError",
    "__version__",
]
