"""Exception hierarchy for the dragonfly_dfcache SDK.

All dfcache-related failures raise one of the :class:`DfcacheError`
subclasses below, so callers can catch them all with a single
``except DfcacheError`` or discriminate with finer-grained handlers.
"""

from __future__ import annotations


class DfcacheError(RuntimeError):
    """Base class for every error raised by the dfcache SDK.

    Inherits from :class:`RuntimeError` so that existing ``except
    RuntimeError`` blocks keep working.
    """


class DfcacheNotFoundError(DfcacheError):
    """Raised when the ``dfcache`` binary cannot be located.

    This typically means Dragonfly's client/dfcache CLI is not
    installed on the system, or it is not present on ``PATH`` and no
    explicit ``binary_path`` was supplied in the config.
    """


class DfcacheTimeoutError(DfcacheError):
    """Raised when a dfcache CLI invocation exceeds the configured timeout."""


class DfcacheCommandError(DfcacheError):
    """Raised when a dfcache CLI command exits with a non-zero status.

    The underlying ``exit_code``, ``stdout`` and ``stderr`` from the
    failed process are retained as attributes so callers can inspect
    them for diagnostics or retries.
    """

    def __init__(self, exit_code: int, stdout: str, stderr: str) -> None:
        self.exit_code = int(exit_code)
        self.stdout = stdout
        self.stderr = stderr
        message = stderr.strip() or stdout.strip() or "dfcache command failed"
        super().__init__(f"dfcache command failed (exit {self.exit_code}): {message}")