"""Configuration for the dfget Python SDK.

:class:`DfgetConfig` captures everything needed to drive the
Dragonfly ``dfget`` CLI.  Configuration can be provided explicitly, or
sourced from environment variables so it can be shared between local
development and CI without code changes.

Precedence, highest first:

1. Explicit fields passed to the constructor (e.g. ``binary=...``).
2. Environment variables (``DFGET_BIN``, ``DFDAEMON_ENDPOINT``,
   ``DFGET_TIMEOUT``, ``DFGET_LOG_LEVEL``) via :meth:`DfgetConfig.from_env`.
3. Defaults (``"2h"`` timeout, ``None`` log level, no explicit binary).
"""

from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, replace as _dataclass_replace
from typing import Any, Dict, Mapping, Optional

DEFAULT_TIMEOUT = "2h"
DFGET_BIN_ENV = "DFGET_BIN"
DFDAEMON_ENDPOINT_ENV = "DFDAEMON_ENDPOINT"
DFGET_TIMEOUT_ENV = "DFGET_TIMEOUT"
DFGET_LOG_LEVEL_ENV = "DFGET_LOG_LEVEL"
VALID_LOG_LEVELS = ("trace", "debug", "info", "warn", "error")


@dataclass(frozen=True)
class DfgetConfig:
    """Configuration for driving the Dragonfly ``dfget`` CLI.

    Attributes:
        binary: Path/name of the ``dfget`` executable.  Resolved
            explicitly, then via ``DFGET_BIN``, then via ``PATH`` lookup.
        endpoint: Endpoint (Unix domain socket path) of the dfdaemon the
            ``dfget`` CLI should talk to.  ``None`` means "not set".
        default_timeout: Default download timeout in human-readable form
            (``humantime`` style, e.g. ``"2h"``).
        log_level: dfget log level (``trace``, ``debug``, ``info``,
            ``warn``, ``error``); ``None`` means unset.
    """

    binary: Optional[str] = None
    endpoint: Optional[str] = None
    default_timeout: str = DEFAULT_TIMEOUT
    log_level: Optional[str] = None

    def __post_init__(self) -> None:
        if self.log_level is not None and self.log_level not in VALID_LOG_LEVELS:
            raise ValueError(f"invalid log level: {self.log_level!r}")

    def replace(self, **changes: Any) -> "DfgetConfig":
        """Return a new config with the given fields overridden."""
        return _dataclass_replace(self, **changes)

    def resolve_binary(self, env: Optional[Mapping[str, str]] = None) -> Optional[str]:
        """Resolve the dfget binary: explicit > ``DFGET_BIN`` > ``PATH`` lookup."""
        source: Dict[str, str] = dict(env) if env is not None else {}
        if self.binary:
            return self.binary
        explicit = source.get(DFGET_BIN_ENV)
        if explicit:
            return explicit
        path_env = source.get("PATH") or os.environ.get("PATH") or ""
        if not path_env:
            return None
        return shutil.which("dfget", path=path_env)

    def resolve_endpoint(self, env: Optional[Mapping[str, str]] = None) -> Optional[str]:
        """Resolve the dfdaemon endpoint: explicit > ``DFDAEMON_ENDPOINT`` > unset."""
        source: Dict[str, str] = dict(env) if env is not None else {}
        if self.endpoint is not None:
            return self.endpoint
        return source.get(DFDAEMON_ENDPOINT_ENV)

    @classmethod
    def from_env(cls, env: Optional[Mapping[str, str]] = None) -> "DfgetConfig":
        """Build a config from an environment mapping (defaults when unset)."""
        source: Dict[str, str] = dict(env) if env is not None else {}
        return cls(
            binary=None,
            endpoint=None,
            default_timeout=str(source.get(DFGET_TIMEOUT_ENV) or DEFAULT_TIMEOUT),
            log_level=source.get(DFGET_LOG_LEVEL_ENV),
        )

    def replace(self, **changes: object) -> "DfgetConfig":
        """Return a new configuration with ``changes`` applied (dataclasses.replace-friendly)."""
        return _dataclass_replace(self, **changes)
