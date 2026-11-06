"""Dragonfly dfget Python SDK.

A lightweight, unofficial Python client for Dragonfly's P2P download
tool (``dfget``), wrapping the ``dfget`` command-line tool via
subprocess.  It provides
:meth:`~dragonfly_dfget.DfgetClient.download`,
:meth:`~dragonfly_dfget.DfgetClient.version` and
:meth:`~dragonfly_dfget.DfgetClient.health` operations, plus
asyncio-native async methods.

The wrapper shells out to the ``dfget`` binary, so you do **not** need
a running Dragonfly cluster to use or test the SDK.
"""

from __future__ import annotations

from .client import DfgetClient, DownloadResult, HealthResult
from .config import DfgetConfig
from .errors import (
    DfgetCommandError,
    DfgetError,
    DfgetNotFoundError,
    DfgetTimeoutError,
)
from ._version import __version__

__all__ = [
    "DfgetClient",
    "DownloadResult",
    "HealthResult",
    "DfgetConfig",
    "DfgetError",
    "DfgetNotFoundError",
    "DfgetTimeoutError",
    "DfgetCommandError",
    "__version__",
]
