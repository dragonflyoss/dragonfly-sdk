"""Dragonfly dfcache Python SDK.

A lightweight, unofficial Python client for Dragonfly's persistent
cache, wrapping the ``dfcache`` CLI via subprocess.  It provides
:meth:`~dragonfly_dfcache.DfcacheClient.stat`,
:meth:`~dragonfly_dfcache.DfcacheClient.import_file`,
:meth:`~dragonfly_dfcache.DfcacheClient.export` and
:meth:`~dragonfly_dfcache.DfcacheClient.delete` operations, plus
health/version checks and asyncio-native async methods.

The wrapper shells out to the ``dfcache`` binary, so you do **not** need
a running Dragonfly cluster to use or test the SDK.
"""

from __future__ import annotations

from .client import DfcacheClient
from .config import DfcacheConfig
from .errors import (
    DfcacheCommandError,
    DfcacheError,
    DfcacheNotFoundError,
    DfcacheTimeoutError,
)
from ._version import __version__

__all__ = [
    "DfcacheClient",
    "DfcacheConfig",
    "DfcacheError",
    "DfcacheNotFoundError",
    "DfcacheTimeoutError",
    "DfcacheCommandError",
    "__version__",
]