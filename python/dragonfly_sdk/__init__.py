"""Dragonfly Python SDK.

A wrapper over the ``dfget`` download client, built on the generated
gRPC bindings published as the ``dragonfly-api`` package.
"""

from .client import (
    DEFAULT_DFDAEMON_SOCKET_PATH,
    DEFAULT_PRIORITY,
    Client,
    DownloadResult,
    build_download,
)
from .errors import DownloadError, DragonflyError

__all__ = [
    "Client",
    "DownloadResult",
    "build_download",
    "DEFAULT_DFDAEMON_SOCKET_PATH",
    "DEFAULT_PRIORITY",
    "DragonflyError",
    "DownloadError",
]

__version__ = "0.1.0"
