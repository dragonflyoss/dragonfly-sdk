"""Core client wrapping the ``dfcache`` CLI via subprocess.

The :class:`DfcacheClient` provides typed, idiomatic Python access to
Dragonfly's persistent cache through the ``dfcache`` command-line tool.
Every high-level method maps to one ``dfcache`` subcommand:

===================  =========================
Python method        ``dfcache`` subcommand
===================  =========================
:meth:`stat`         ``dfcache stat <ref>``
:meth:`import_file`  ``dfcache import <file> ...``
:meth:`export`       ``dfcache export <task> ...``
:meth:`delete`       ``dfcache delete <task>``
:meth:`version`      ``dfcache version``
:meth:`health`       version/stat probe
===================  =========================

Both synchronous (``stat``, ``import_file`` ...) and asyncio-native
(``astat``, ``aimport`` ...) variants are provided.  The CLI is invoked
via :mod:`subprocess`/:mod:`asyncio`, so **no** cluster or daemon is
required to import or test the SDK.
"""

from __future__ import annotations

import asyncio
import json
import os
import re
import shutil
import subprocess
from typing import Any, Dict, List, Optional, Sequence, Tuple

from .config import DfcacheConfig
from .errors import (
    DfcacheCommandError,
    DfcacheError,
    DfcacheNotFoundError,
    DfcacheTimeoutError,
)

__all__ = ["DfcacheClient"]

# Exit code dfcache/the OS commonly uses when a binary cannot be found.
_EACCES_RETURNCODES = {125, 126, 127, 9009}


def _decode(data) -> str:
    """Decode bytes output to ``str`` without erroring on bad content."""
    if isinstance(data, bytes):
        return data.decode(encoding="utf-8", errors="replace")
    return "" if data is None else str(data)


class _OutputParser:
    """Parse dfcache CLI stdout/stderr into a dict for high-level methods."""

    @staticmethod
    def parse(stdout: str, stderr: str) -> Dict[str, Any]:
        """Return the first structured blob found in stdout or stderr."""
        for source in (stdout, stderr):
            blob = (source or "").strip()
            if blob.startswith("{"):
                try:
                    return json.loads(blob)
                except json.JSONDecodeError:
                    pass
            as_dict = _OutputParser._key_values(blob)
            if as_dict:
                return as_dict
        return {}

    @staticmethod
    def _key_values(text: str) -> Dict[str, Any]:
        """Parse ``key = value`` styled output into a dict."""
        result: Dict[str, Any] = {}
        for raw_line in (text or "").splitlines():
            line = raw_line.strip()
            if not line or line.startswith("#") or "=" not in line:
                continue
            key, _, value = line.partition("=")
            key = key.strip()
            if not key:
                continue
            value = value.strip().strip('"').strip("'")
            result[key] = _OutputParser._coerce(value)
        return result

    @staticmethod
    def _coerce(value: str) -> Any:
        """Coerce a scalar string to bool/int/float where unambiguous."""
        lowered = value.lower()
        if lowered == "true":
            return True
        if lowered == "false":
            return False
        if lowered in {"null", "none", "nil"}:
            return None
        try:
            if re.fullmatch(r"-?\d+", value):
                return int(value)
            if re.fullmatch(r"-?\d*\.\d+", value):
                return float(value)
        except ValueError:
            pass
        return value


class DfcacheClient:
    """Client for Dragonfly persistent cache via the ``dfcache`` CLI.

    Args:
        config: Optional :class:`DfcacheConfig`.  A default config is
            created when omitted.
    """

    def __init__(self, config: Optional[DfcacheConfig] = None) -> None:
        self.config = config or DfcacheConfig()

    # ------------------------------------------------------------------
    # Command construction
    # ------------------------------------------------------------------
    def _build_command(self, subcommand: str, *args: str) -> List[str]:
        """Assemble the full argv for a ``dfcache`` invocation."""
        return [self.config.binary, subcommand, *args]

    def _base_overrides(self) -> List[str]:
        """Return global connection flags based on the config.

        These mirror dfcache's ``--config``/endpoint and TLS flags.  Only
        flags the CLI supports are emitted; unknown flags are ignored
        gracefully at the CLI level by keeping them well-formed.
        """
        flags: List[str] = []
        # Endpoint / service config
        if self.config.endpoint:
            flags.extend(["--endpoint", self.config.endpoint])
        # TLS
        if self.config.tls:
            flags.append("--tls")
            if self.config.tls_ca_cert:
                flags.extend(["--tls-ca-cert", self.config.tls_ca_cert])
        return flags

    # ------------------------------------------------------------------
    # Low-level execution
    # ------------------------------------------------------------------
    def _ensure_binary(self) -> None:
        """Raise :class:`DfcacheNotFoundError` if the configured binary is missing.

        A bare name (e.g. ``dfcache``) is left to be resolved by
        ``subprocess`` so PATH lookup still applies.  A path containing a
        directory separator is checked for existence so mistakes surface
        early.
        """
        path = self.config.binary_path or ""
        if not path:
            return
        if os.path.isabs(path):
            if not os.path.isfile(path):
                raise DfcacheNotFoundError(
                    f"dfcache binary not found at configured path: {path!r}"
                )
            return
        if os.sep in path or "/" in path:
            if shutil.which(path) is None:
                raise DfcacheNotFoundError(
                    f"dfcache binary not found for configured name: {path!r}"
                )

    def _run_sync(
        self, command: Sequence[str], timeout: Optional[float] = None
    ) -> Tuple[int, str, str]:
        """Run ``command`` synchronously, returning ``(code, stdout, stderr)``.

        Raises:
            DfcacheTimeoutError: on timeout.
            DfcacheCommandError: on a non-zero exit status.
        """
        self._ensure_binary()
        effective_timeout = self.config.timeout if timeout is None else timeout
        try:
            result = subprocess.run(
                command,
                capture_output=True,
                text=True,
                timeout=effective_timeout,
            )
        except FileNotFoundError as exc:
            raise DfcacheNotFoundError(
                f"dfcache binary not found: {command[0]!r}"
            ) from exc
        except subprocess.TimeoutExpired as exc:
            raise DfcacheTimeoutError(
                f"dfcache command timed out after {effective_timeout}s: "
                f"{' '.join(command)}"
            ) from exc

        if result.returncode in _EACCES_RETURNCODES:
            raise DfcacheNotFoundError(
                f"dfcache binary was not found or could not be executed "
                f"(exit {result.returncode}): {command[0]!r}"
            )
        if result.returncode != 0:
            raise DfcacheCommandError(
                exit_code=result.returncode,
                stdout=_decode(result.stdout),
                stderr=_decode(result.stderr),
            )
        return result.returncode, _decode(result.stdout), _decode(result.stderr)

    async def _run_async(
        self, command: Sequence[str], timeout: Optional[float] = None
    ) -> Tuple[int, str, str]:
        """Run ``command`` asynchronously, returning ``(code, stdout, stderr)``.

        Raises:
            DfcacheTimeoutError: on timeout.
            DfcacheCommandError: on a non-zero exit status.
        """
        self._ensure_binary()
        effective_timeout = self.config.timeout if timeout is None else timeout

        async def _spawn() -> asyncio.subprocess.Process:
            return await asyncio.create_subprocess_exec(
                *command,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )

        try:
            proc = await asyncio.wait_for(_spawn(), timeout=effective_timeout)
            stdout, stderr = await asyncio.wait_for(
                proc.communicate(), timeout=effective_timeout
            )
        except FileNotFoundError as exc:
            raise DfcacheNotFoundError(
                f"dfcache binary not found: {command[0]!r}"
            ) from exc
        except asyncio.TimeoutError as exc:
            raise DfcacheTimeoutError(
                f"dfcache command timed out after {effective_timeout}s: "
                f"{' '.join(command)}"
            ) from exc

        returncode = proc.returncode
        out_str = _decode(stdout)
        err_str = _decode(stderr)
        if returncode in _EACCES_RETURNCODES:
            raise DfcacheNotFoundError(
                f"dfcache binary was not found or could not be executed "
                f"(exit {returncode}): {command[0]!r}"
            )
        if returncode != 0:
            raise DfcacheCommandError(exit_code=returncode, stdout=out_str, stderr=err_str)
        return returncode, out_str, err_str

    # ------------------------------------------------------------------
    # High-level synchronous API
    # ------------------------------------------------------------------
    def stat(self, ref: str, timeout: Optional[float] = None) -> Dict[str, Any]:
        """Query cache metadata for ``ref`` (a task ID or file path).

        Args:
            ref: A persistent-cache task ID or a path to stat.
            timeout: Optional per-call timeout in seconds.

        Returns:
            A dictionary of stat attributes parsed from the CLI output.

        Raises:
            DfcacheNotFoundError: if the binary is missing.
            DfcacheTimeoutError: on timeout.
            DfcacheCommandError: on non-zero exit.
        """
        command = self._build_command("stat", ref, *self._base_overrides())
        _, stdout, stderr = self._run_sync(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    def import_file(
        self,
        file_path: str,
        persistent_replica_count: int = 1,
        content_for_calculating_task_id: Optional[str] = None,
        timeout: Optional[float] = None,
    ) -> Dict[str, Any]:
        """Import ``file_path`` into the persistent cache.

        Args:
            file_path: Local path of the file to import.
            persistent_replica_count: Number of persistent replicas the
                cache should hold for the imported content (default ``1``).
            content_for_calculating_task_id: Optional SHA-256 used to derive
                the task ID of the imported content.
            timeout: Optional per-call timeout in seconds.

        Returns:
            A dictionary (usually containing ``task_id``) parsed from the CLI.

        Raises:
            DfcacheNotFoundError / DfcacheTimeoutError / DfcacheCommandError
        """
        command = self._build_command(
            "import",
            file_path,
            f"--persistent-replica-count={persistent_replica_count}",
        )
        if content_for_calculating_task_id:
            command.append(
                f"--content-for-calculating-task-id={content_for_calculating_task_id}"
            )
        command.extend(self._base_overrides())
        _, stdout, stderr = self._run_sync(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    def export(
        self,
        task_id: str,
        output_path: str,
        transfer_from_dfdaemon: bool = False,
        timeout: Optional[float] = None,
    ) -> Dict[str, Any]:
        """Export a previously imported task's content to ``output_path``.

        Args:
            task_id: The task ID to export.
            output_path: Local destination path for the exported content.
            transfer_from_dfdaemon: When True, request the content directly
                from dfdaemon rather than the manager.
            timeout: Optional per-call timeout in seconds.

        Returns:
            A dictionary (usually containing ``output_path``) from the CLI.

        Raises:
            DfcacheNotFoundError / DfcacheTimeoutError / DfcacheCommandError
        """
        command = self._build_command(
            "export",
            task_id,
            f"--output={output_path}",
        )
        if transfer_from_dfdaemon:
            command.append("--transfer-from-dfdaemon")
        command.extend(self._base_overrides())
        _, stdout, stderr = self._run_sync(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    def delete(self, task_id: str, timeout: Optional[float] = None) -> Dict[str, Any]:
        """Delete ``task_id``'s content from the persistent cache.

        Args:
            task_id: The task ID to delete.
            timeout: Optional per-call timeout in seconds.

        Returns:
            A dictionary (e.g. ``{"task_id": ..., "status": "deleted"}``).

        Raises:
            DfcacheNotFoundError / DfcacheTimeoutError / DfcacheCommandError
        """
        command = self._build_command("delete", task_id, *self._base_overrides())
        _, stdout, stderr = self._run_sync(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    def version(self, timeout: Optional[float] = None) -> Dict[str, Any]:
        """Return dfcache CLI version information.

        Returns:
            A dictionary such as ``{"version": "v1.0.0"}``.

        Raises:
            DfcacheNotFoundError / DfcacheTimeoutError / DfcacheCommandError
        """
        command = self._build_command("version", *self._base_overrides())
        _, stdout, stderr = self._run_sync(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr) or _extract_version(stdout, stderr)

    def health(self, timeout: Optional[float] = None) -> bool:
        """Probe whether the dfcache CLI is usable.

        Returns ``True`` if the version probe succeeds, ``False``
        otherwise.  Never raises.

        Args:
            timeout: Optional per-call timeout in seconds.
        """
        try:
            self.version(timeout=timeout)
            return True
        except DfcacheError:
            return False

    # ------------------------------------------------------------------
    # High-level asyncio API
    # ------------------------------------------------------------------
    async def astat(self, ref: str, timeout: Optional[float] = None) -> Dict[str, Any]:
        """Asyncio variant of :meth:`stat`."""
        command = self._build_command("stat", ref, *self._base_overrides())
        _, stdout, stderr = await self._run_async(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    async def aimport(
        self,
        file_path: str,
        persistent_replica_count: int = 1,
        content_for_calculating_task_id: Optional[str] = None,
        timeout: Optional[float] = None,
    ) -> Dict[str, Any]:
        """Asyncio variant of :meth:`import_file`."""
        command = self._build_command(
            "import",
            file_path,
            f"--persistent-replica-count={persistent_replica_count}",
        )
        if content_for_calculating_task_id:
            command.append(
                f"--content-for-calculating-task-id={content_for_calculating_task_id}"
            )
        command.extend(self._base_overrides())
        _, stdout, stderr = await self._run_async(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    async def aexport(
        self,
        task_id: str,
        output_path: str,
        transfer_from_dfdaemon: bool = False,
        timeout: Optional[float] = None,
    ) -> Dict[str, Any]:
        """Asyncio variant of :meth:`export`."""
        command = self._build_command(
            "export",
            task_id,
            f"--output={output_path}",
        )
        if transfer_from_dfdaemon:
            command.append("--transfer-from-dfdaemon")
        command.extend(self._base_overrides())
        _, stdout, stderr = await self._run_async(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    async def adelete(
        self, task_id: str, timeout: Optional[float] = None
    ) -> Dict[str, Any]:
        """Asyncio variant of :meth:`delete`."""
        command = self._build_command("delete", task_id, *self._base_overrides())
        _, stdout, stderr = await self._run_async(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr)

    async def aversion(self, timeout: Optional[float] = None) -> Dict[str, Any]:
        """Asyncio variant of :meth:`version`."""
        command = self._build_command("version", *self._base_overrides())
        _, stdout, stderr = await self._run_async(command, timeout=timeout)
        return _OutputParser.parse(stdout, stderr) or _extract_version(stdout, stderr)

    async def ahealth(self, timeout: Optional[float] = None) -> bool:
        """Asyncio variant of :meth:`health`.  Never raises."""
        try:
            await self.aversion(timeout=timeout)
            return True
        except DfcacheError:
            return False


def _extract_version(stdout: str, stderr: str) -> Dict[str, Any]:
    """Best-effort extract of a version string from human output."""
    output = (stdout or stderr or "").strip()
    if not output:
        return {}
    match = re.search(r"(\d+\.\d+(?:\.\d+)?(?:[-+][A-Za-z0-9.]+)?)", output)
    if match:
        return {"version": match.group(1)}
    return {} if not output else {"version": output.splitlines()[0].strip()}