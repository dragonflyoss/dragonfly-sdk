"""Configuration for the dfcache Python SDK.

:class:`DfcacheConfig` captures everything needed to talk to the
Dragonfly persistent cache through the ``dfcache`` CLI.  Configuration
can be provided explicitly, or sourced from environment variables so it
can be shared between local development and CI without code changes.
"""

from __future__ import annotations

import os
import shutil
from dataclasses import dataclass, field
from typing import Optional

DEFAULT_ENDPOINT = "http://localhost:8080"
DEFAULT_TIMEOUT = 30.0

# Environment variables used to override config defaults.
ENV_ENDPOINT = "DFCACHE_ENDPOINT"
ENV_TLS = "DFCACHE_TLS"
ENV_TLS_CA_CERT = "DFCACHE_TLS_CA_CERT"
ENV_TIMEOUT = "DFCACHE_TIMEOUT"
ENV_BINARY = "DFCACHE_BINARY"


def resolve_endpoint() -> str:
    """Return the endpoint from ``DFCACHE_ENDPOINT`` or the default."""
    return os.environ.get(ENV_ENDPOINT) or DEFAULT_ENDPOINT


def resolve_timeout() -> float:
    """Return the timeout from ``DFCACHE_TIMEOUT`` or the default.

    Raises:
        ValueError: if the environment value is not a valid number.
    """
    raw = os.environ.get(ENV_TIMEOUT)
    if not raw:
        return DEFAULT_TIMEOUT
    value = float(raw)
    if value <= 0:
        raise ValueError(f"{ENV_TIMEOUT} must be a positive number, got {value!r}")
    return value


def resolve_binary() -> str:
    """Return the dfcache binary path.

    Honors ``DFCACHE_BINARY`` first, then falls back to ``dfcache`` /
    ``dfcache.exe`` found on ``PATH``, and finally to the plain
    ``dfcache`` name (letting ``subprocess`` resolve or fail on PATH).
    """
    explicit = os.environ.get(ENV_BINARY)
    if explicit:
        return explicit
    return shutil.which("dfcache") or shutil.which("dfcache.exe") or "dfcache"


@dataclass
class DfcacheConfig:
    """Configuration for connecting to Dragonfly persistent cache.

    Attributes:
        endpoint: Base address of the Dragonfly manager / dfdaemon the
            ``dfcache`` CLI should talk to.  ``None`` (default) resolves
            from ``DFCACHE_ENDPOINT`` or ``http://localhost:8080``; an
            explicit empty string suppresses the ``--endpoint`` flag.
        tls: Whether to enable TLS for the connection.
        tls_ca_cert: Optional path to a CA certificate used to verify the
            TLS peer.
        timeout: Per-command subprocess timeout in seconds.
        binary_path: Path to the ``dfcache`` executable.  Resolved
            automatically when not supplied (see :func:`resolve_binary`).
        env: Mapping used to seed environment-variable based defaults.  For
            tests and programmatic configuration; defaults to
            ``os.environ``.
    """

    endpoint: Optional[str] = None
    tls: bool = False
    tls_ca_cert: Optional[str] = None
    timeout: float = field(default_factory=resolve_timeout)
    binary_path: Optional[str] = None
    env: dict = field(default_factory=dict, repr=False)

    def __post_init__(self) -> None:
        source = self.env or os.environ
        # ``endpoint=None`` means "resolve me" (env first, then default);
        # an explicit empty string means "do not emit an --endpoint flag".
        if self.endpoint is None:
            self.endpoint = source.get(ENV_ENDPOINT) or DEFAULT_ENDPOINT
        tls_raw = source.get(ENV_TLS)
        if tls_raw is not None:
            self.tls = tls_raw.strip().lower() in {"1", "true", "yes", "on"}
        if self.tls_ca_cert is None:
            self.tls_ca_cert = source.get(ENV_TLS_CA_CERT)
        if self.timeout <= 0:
            raise ValueError(f"timeout must be a positive number, got {self.timeout!r}")
        if not self.binary_path:
            explicit = source.get(ENV_BINARY)
            self.binary_path = (
                explicit
                or shutil.which("dfcache")
                or shutil.which("dfcache.exe")
                or "dfcache"
            )

    @property
    def binary(self) -> str:
        """Return the resolved dfcache executable path/name."""
        return self.binary_path or "dfcache"

    def __repr__(self) -> str:
        cls = type(self).__name__
        return (
            f"{cls}(endpoint={self.endpoint!r}, tls={self.tls}, "
            f"tls_ca_cert={self.tls_ca_cert!r}, timeout={self.timeout!r}, "
            f"binary_path={self.binary_path!r})"
        )