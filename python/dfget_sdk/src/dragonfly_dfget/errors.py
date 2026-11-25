"""Exception hierarchy for the dragonfly_dfget SDK.

All dfget-related failures raise one of the :class:`DfgetError`
subclasses below, so callers can catch them all with a single
``except DfgetError`` or discriminate with finer-grained handlers.
"""

from __future__ import annotations

from typing import List, Optional


class DfgetError(Exception):
    """Base class for every error raised by the dfget SDK."""


class DfgetNotFoundError(DfgetError):
    """Raised when the ``dfget`` binary cannot be located."""

    def __init__(
        self, message: str = "dfget binary not found", binary: Optional[str] = None
    ) -> None:
        self.binary = binary
        super().__init__(message)


class DfgetTimeoutError(DfgetError):
    """Raised when a dfget CLI invocation exceeds the configured timeout."""

    def __init__(
        self,
        message: str = "dfget command timed out",
        timeout_s: Optional[float] = None,
        argv: Optional[List[str]] = None,
    ) -> None:
        self.timeout_s = timeout_s
        self.argv = list(argv) if argv is not None else []
        super().__init__(message)


class DfgetCommandError(DfgetError):
    """Raised when a dfget CLI command exits with a non-zero status."""

    def __init__(
        self,
        message: str,
        returncode: int,
        stdout: str,
        stderr: str,
        argv: Optional[List[str]] = None,
    ) -> None:
        self.returncode = int(returncode)
        self.stdout = stdout
        self.stderr = stderr
        self.argv = list(argv) if argv is not None else []
        detail = (stderr or stdout or message or "").strip()
        super().__init__(f"dfget command failed (exit code {self.returncode}): {detail}")
