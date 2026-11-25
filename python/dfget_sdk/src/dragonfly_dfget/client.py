"""Core client wrapping the ``dfget`` CLI via subprocess.

The :class:`DfgetClient` provides typed, idiomatic Python access to
Dragonfly's P2P download tool through the ``dfget`` command-line
executable.  Every high-level method maps to one ``dfget`` invocation:

======================  ==========================
Python method           ``dfget`` invocation
======================  ==========================
:meth:`download`        ``dfget <url> -O <output> ...``
:meth:`version`         ``dfget --version``
:meth:`health`          ``dfget --version`` (binary probe)
======================  ===========================

Both synchronous (``download``, ``version``, ``health``) and
asyncio-native (``adownload``, ``aversion``, ``ahealth``) variants are
provided.  The CLI is invoked via :mod:`subprocess`/:mod:`asyncio`, so
**no** cluster or daemon is required to import or test the SDK.
"""

from __future__ import annotations

import asyncio
import os
import re
import shutil
import subprocess
import time
from dataclasses import dataclass
from typing import Any, Dict, List, Mapping, Optional, Sequence, Tuple

from .config import DfgetConfig
from .errors import DfgetCommandError, DfgetError, DfgetNotFoundError, DfgetTimeoutError

__all__ = ["DfgetClient", "DownloadResult", "HealthResult"]


DEFAULT_DOWNLOAD_TIMEOUT_SECONDS = 7200.0
_EACCES_RETURNCODES = {125, 126, 127, 9009}

_TASK_ID_RE = re.compile(
    r"task[\s_-]*id[\s:=]+['\"]?\s*("
    r"[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}"
    r"|[0-9a-fA-F]{16,64}"
    r")",
    re.IGNORECASE,
)

_UNIT_MULTIPLIERS = {
    "": 1,
    "b": 1,
    "byte": 1,
    "bytes": 1,
    "k": 1024,
    "kb": 1024,
    "kib": 1024,
    "m": 1024 ** 2,
    "mb": 1024 ** 2,
    "mib": 1024 ** 2,
    "g": 1024 ** 3,
    "gb": 1024 ** 3,
    "gib": 1024 ** 3,
    "t": 1024 ** 4,
    "tb": 1024 ** 4,
    "tib": 1024 ** 4,
}


def _decode(data: Any) -> str:
    """Decode bytes output to ``str`` without erroring on bad content."""
    if isinstance(data, bytes):
        return data.decode(encoding="utf-8", errors="replace")
    return "" if data is None else str(data)


def parse_task_id(text: str) -> Optional[str]:
    """Extract a task id from dfget CLI output, if present."""
    match = _TASK_ID_RE.search(text or "")
    if match is None:
        return None
    return match.group(1)


_SIZE_LINE_KEYWORDS = ("download", "size")


def parse_downloaded_size(text: str) -> Optional[int]:
    """Extract a downloaded size (bytes) from dfget CLI output.

    Only lines that report a download progress or size are considered,
    so surrounding log chatter (task ids, progress bars, ... ) is ignored.
    """
    if not text:
        return None
    for raw_line in str(text).splitlines():
        line = raw_line.strip().lower()
        if not line:
            continue
        if not any(keyword in line for keyword in _SIZE_LINE_KEYWORDS):
            continue
        cleaned = line.replace(",", "")
        match = re.search(r"([0-9]*\.?[0-9]+)\s*([a-z]*)", cleaned)
        if match is None:
            continue
        try:
            value = float(match.group(1))
        except ValueError:
            return None
        unit = match.group(2)
        multiplier = _UNIT_MULTIPLIERS.get(unit, 1)
        return int(round(value * multiplier))
    return None


def parse_progress_lines(text: str) -> Tuple[str, ...]:
    """Return progress-bar lines found in dfget CLI output."""
    lines: List[str] = []
    for raw_line in (text or "").splitlines():
        line = raw_line.strip()
        if not line or "%" not in line:
            continue
        lines.append(line)
    return tuple(lines)


def _extract_version(stdout: str, stderr: str) -> str:
    """Best-effort extract of the version string from CLI output."""
    output = (_decode(stdout) or _decode(stderr) or "").strip()
    return output


def _parse_duration(value: Any) -> float:
    """Parse a human-readable duration (``"2h"``, ``"45s"``, ``"30m"``) to seconds."""
    text = str(value or DEFAULT_DOWNLOAD_TIMEOUT_SECONDS).strip().lower()
    if not text:
        return DEFAULT_DOWNLOAD_TIMEOUT_SECONDS
    if text.isdigit():
        return float(text)
    match = re.fullmatch(r"(?:(\d+(?:\.\d+)?)h)|(?:(\d+(?:\.\d+)?)m)|(?:(\d+(?:\.\d+)?)s)", text)
    if match is None:
        return DEFAULT_DOWNLOAD_TIMEOUT_SECONDS
    if match.group(1) is not None:
        return float(match.group(1)) * 3600.0
    if match.group(2) is not None:
        return float(match.group(2)) * 60.0
    return float(match.group(3))


@dataclass
class DownloadResult:
    """Typed outcome of a single :meth:`DfgetClient.download` invocation."""

    output_path: str = ""
    task_id: Optional[str] = None
    raw_stdout: str = ""
    raw_stderr: str = ""
    duration_s: float = 0.0
    downloaded_size: Optional[int] = None
    progress_lines: Tuple[str, ...] = ()
    returncode: int = 0


@dataclass
class HealthResult:
    """Outcome of a :meth:`DfgetClient.health` probe."""

    healthy: bool = True
    binary: Optional[str] = None
    version: Optional[str] = None
    message: str = "ok"

    def as_dict(self) -> Dict[str, Any]:
        """Return the health payload as a plain dictionary."""
        return {
            "healthy": self.healthy,
            "binary": self.binary,
            "version": self.version,
            "message": self.message,
        }


class DfgetClient:
    """Client for Dragonfly's P2P download tool via the ``dfget`` CLI.

    Args:
        config: Optional :class:`DfgetConfig`.  A default config is
            created when omitted.
        env: Optional environment mapping used for config resolution
            (binary / endpoint).  Defaults to an empty mapping when omitted.
    """

    def __init__(
        self, config: Optional[DfgetConfig] = None, env: Optional[Mapping[str, str]] = None
    ) -> None:
        self.config = config or DfgetConfig()
        self.env: Dict[str, str] = dict(env) if env is not None else {}

    # ------------------------------------------------------------------
    # Resolution helpers
    # ------------------------------------------------------------------
    def _resolve_binary(self) -> Optional[str]:
        resolved = self.config.resolve_binary(self.env)
        if resolved:
            return resolved
        return self.config.binary

    def _resolve_endpoint(self) -> Optional[str]:
        return self.config.resolve_endpoint(self.env)

    def _ensure_binary(self) -> str:
        binary = self._resolve_binary()
        if not binary:
            raise DfgetNotFoundError("dfget binary not found")
        return binary

    # ------------------------------------------------------------------
    # Command construction
    # ------------------------------------------------------------------
    def build_download_argv(
        self, url: str, output: str, **kwargs: Any
    ) -> List[str]:
        """Assemble the full argv for a ``dfget`` download invocation."""
        binary = self._ensure_binary()
        argv: List[str] = [binary, url, "-O", output]
        endpoint = self._resolve_endpoint()
        if endpoint:
            argv += ["-e", endpoint]
        if kwargs.get("overwrite"):
            argv.append("--overwrite")
        if kwargs.get("force_hard_link"):
            argv.append("--force-hard-link")
        if kwargs.get("recursive"):
            argv.append("--recursive")
        argv += ["--timeout", str(kwargs.get("timeout") or self.config.default_timeout)]
        if kwargs.get("digest"):
            argv += ["--digest", str(kwargs["digest"])]
        if kwargs.get("transfer_from_dfdaemon"):
            argv.append("--transfer-from-dfdaemon")
        if kwargs.get("content_for_calculating_task_id"):
            argv += [
                "--content-for-calculating-task-id",
                str(kwargs["content_for_calculating_task_id"]),
            ]
        if kwargs.get("tag"):
            argv += ["--tag", str(kwargs["tag"])]
        if kwargs.get("application"):
            argv += ["--application", str(kwargs["application"])]
        if kwargs.get("priority") is not None:
            argv += ["--priority", str(kwargs["priority"])]
        if kwargs.get("background") is True:
            argv.append("--background")
        if self.config.log_level:
            argv += ["--log-level", str(self.config.log_level)]
        argv.append("--console")
        extra_args = kwargs.get("extra_args")
        if extra_args:
            argv += [str(arg) for arg in extra_args]
        return argv

    # ------------------------------------------------------------------
    # Low-level execution
    # ------------------------------------------------------------------
    def _effective_timeout(self, timeout_s: Optional[float]) -> float:
        """Per-call timeout in seconds; falls back to the configured default."""
        if timeout_s is not None:
            return float(timeout_s)
        return _parse_duration(self.config.default_timeout)

    def _run_sync(
        self, command: Sequence[str], timeout: Optional[float] = None
    ) -> "subprocess.CompletedProcess":
        """Run ``command`` synchronously."""
        binary = self._ensure_binary()
        effective_timeout = _parse_duration(self.config.default_timeout) if timeout is None else timeout
        try:
            result = subprocess.run(
                list(command),
                capture_output=True,
                text=True,
                timeout=effective_timeout,
            )
        except FileNotFoundError as exc:
            raise DfgetNotFoundError("dfget binary not found") from exc
        except subprocess.TimeoutExpired as exc:
            raise DfgetTimeoutError(
                "dfget command timed out", timeout_s=effective_timeout, argv=list(command)
            ) from exc
        if result.returncode in _EACCES_RETURNCODES:
            raise DfgetNotFoundError("dfget binary not found or not executable")
        if result.returncode != 0:
            raise DfgetCommandError(
                "dfget command failed",
                returncode=result.returncode,
                stdout=_decode(result.stdout),
                stderr=_decode(result.stderr),
                argv=list(command),
            )
        return result

    async def _run_async(
        self, command: Sequence[str], timeout: Optional[float] = None
    ) -> "Tuple[str, str, Any]":
        """Run ``command`` asynchronously, returning ``(stdout, stderr, proc)``.

        Raises:
            DfgetNotFoundError: if the binary is missing.
            DfgetTimeoutError: on timeout.
            DfgetCommandError: on a non-zero exit status.
        """
        binary = self._ensure_binary()
        effective_timeout = _parse_duration(self.config.default_timeout) if timeout is None else timeout

        async def _spawn() -> "asyncio.subprocess.Process":
            return await asyncio.create_subprocess_exec(
                *command,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )

        proc = await asyncio.wait_for(_spawn(), timeout=effective_timeout)
        stdout, stderr = await asyncio.wait_for(proc.communicate(), timeout=effective_timeout)
        if proc.returncode in _EACCES_RETURNCODES:
            raise DfgetNotFoundError("dfget binary not found or not executable")
        if proc.returncode != 0:
            raise DfgetCommandError(
                "dfget command failed",
                returncode=proc.returncode,
                stdout=_decode(stdout),
                stderr=_decode(stderr),
                argv=list(command),
            )
        return _decode(stdout), _decode(stderr), proc

    # ------------------------------------------------------------------
    # High-level synchronous API
    # ------------------------------------------------------------------
    def download(
        self,
        url: str,
        output: str,
        *,
        overwrite: bool = False,
        force_hard_link: bool = False,
        recursive: bool = False,
        timeout: str = "2h",
        digest: Optional[str] = None,
        transfer_from_dfdaemon: bool = False,
        content_for_calculating_task_id: Optional[str] = None,
        tag: Optional[str] = None,
        application: Optional[str] = None,
        priority: Optional[int] = None,
        background: Optional[bool] = None,
        extra_args: Optional[Sequence[str]] = None,
        timeout_s: Optional[float] = None,
    ) -> DownloadResult:
        """Download ``url`` to ``output`` through the dfget CLI.

        Args:
            url: URL to download, e.g. ``huggingface://Qwen/Qwen2.5-7B/config.json``.
            output: Local path the downloaded file should be written to.
            overwrite: Overwrite the output file if it already exists.
            force_hard_link: Fail the download when a hard link cannot be created.
            recursive: Download a directory tree recursively.
            timeout: Download timeout in human-readable duration form
                (``humantime`` style, e.g. ``"2h"``).
            digest: Optional integrity digest, ``<algorithm>:<digest>``.
            transfer_from_dfdaemon: Transfer the piece content through the
                dfdaemon unix domain socket instead of letting dfdaemon
                hardlink/copy into the output path.
            content_for_calculating_task_id: Content used to calculate the
                task id when set.
            tag: Task tag; different tags for the same URL yield different tasks.
            application: Application grouping for the same URL.
            priority: Scheduling priority (dfget default: 6).
            background: Reserved CLI flag passthrough; when ``True`` the
                download is requested in the background.
            extra_args: Additional raw CLI arguments appended to the
                dfget invocation.
            timeout_s: Optional per-call subprocess timeout in seconds;
                overrides the configured timeout.

        Returns:
            A :class:`DownloadResult` with the parsed task id and raw CLI
            output plus the measured duration.

        Raises:
            DfgetNotFoundError: if the binary is missing.
            DfgetTimeoutError: on timeout.
            DfgetCommandError: on non-zero exit.
        """
        argv = self.build_download_argv(
            url,
            output,
            overwrite=overwrite,
            force_hard_link=force_hard_link,
            recursive=recursive,
            timeout=timeout,
            digest=digest,
            transfer_from_dfdaemon=transfer_from_dfdaemon,
            content_for_calculating_task_id=content_for_calculating_task_id,
            tag=tag,
            application=application,
            priority=priority,
            background=background,
            extra_args=extra_args,
        )
        start = time.monotonic()
        try:
            result = subprocess.run(
                argv,
                capture_output=True,
                text=True,
                timeout=self._effective_timeout(timeout_s),
            )
        except FileNotFoundError as exc:
            raise DfgetNotFoundError("dfget binary not found") from exc
        except subprocess.TimeoutExpired as exc:
            raise DfgetTimeoutError(
                "dfget command timed out", timeout_s=self._effective_timeout(timeout_s), argv=argv
            ) from exc
        duration_s = time.monotonic() - start

        if result.returncode != 0:
            raise DfgetCommandError(
                "dfget command failed",
                returncode=result.returncode,
                stdout=_decode(result.stdout),
                stderr=_decode(result.stderr),
                argv=argv,
            )

        stdout = _decode(result.stdout)
        stderr = _decode(result.stderr)
        return DownloadResult(
            output_path=os.path.abspath(output),
            task_id=parse_task_id(stdout) or parse_task_id(stderr),
            raw_stdout=stdout,
            raw_stderr=stderr,
            duration_s=duration_s,
            downloaded_size=parse_downloaded_size(stdout) or parse_downloaded_size(stderr),
            progress_lines=parse_progress_lines(stdout) or parse_progress_lines(stderr),
            returncode=result.returncode,
        )

    async def adownload(
        self,
        url: str,
        output: str,
        *,
        overwrite: bool = False,
        force_hard_link: bool = False,
        recursive: bool = False,
        timeout: str = "2h",
        digest: Optional[str] = None,
        transfer_from_dfdaemon: bool = False,
        content_for_calculating_task_id: Optional[str] = None,
        tag: Optional[str] = None,
        application: Optional[str] = None,
        priority: Optional[int] = None,
        background: Optional[bool] = None,
        extra_args: Optional[Sequence[str]] = None,
        timeout_s: Optional[float] = None,
    ) -> DownloadResult:
        """Asyncio-native twin of :meth:`download`."""
        argv = self.build_download_argv(
            url,
            output,
            overwrite=overwrite,
            force_hard_link=force_hard_link,
            recursive=recursive,
            timeout=timeout,
            digest=digest,
            transfer_from_dfdaemon=transfer_from_dfdaemon,
            content_for_calculating_task_id=content_for_calculating_task_id,
            tag=tag,
            application=application,
            priority=priority,
            background=background,
            extra_args=extra_args,
        )
        start = time.monotonic()
        try:
            communicate_result = await self._run_async(argv, timeout=self._effective_timeout(timeout_s))
        except asyncio.TimeoutError as exc:
            raise DfgetTimeoutError(
                "dfget command timed out",
                timeout_s=self._effective_timeout(timeout_s),
                argv=argv,
            ) from exc
        duration_s = time.monotonic() - start
        stdout, stderr = _decode(communicate_result[0]), _decode(communicate_result[1])
        if communicate_result[2].returncode != 0:
            raise DfgetCommandError(
                "dfget command failed",
                returncode=communicate_result[2].returncode,
                stdout=stdout,
                stderr=stderr,
                argv=argv,
            )
        return DownloadResult(
            output_path=os.path.abspath(output),
            task_id=parse_task_id(stdout) or parse_task_id(stderr),
            raw_stdout=stdout,
            raw_stderr=stderr,
            duration_s=duration_s,
            downloaded_size=parse_downloaded_size(stdout) or parse_downloaded_size(stderr),
            progress_lines=parse_progress_lines(stdout) or parse_progress_lines(stderr),
            returncode=communicate_result[2].returncode,
        )

    # ------------------------------------------------------------------
    # High-level synchronous API: version / health
    # ------------------------------------------------------------------
    def version(self, timeout: Optional[float] = None) -> str:
        """Return the dfget CLI version string."""
        binary = self._ensure_binary()
        command = [binary, "--version"]
        effective_timeout = _parse_duration(self.config.default_timeout) if timeout is None else timeout
        try:
            result = subprocess.run(
                command,
                capture_output=True,
                text=True,
                timeout=effective_timeout,
            )
        except FileNotFoundError as exc:
            raise DfgetNotFoundError("dfget binary not found") from exc
        except subprocess.TimeoutExpired as exc:
            raise DfgetTimeoutError(
                "dfget command timed out", timeout_s=effective_timeout, argv=command
            ) from exc
        if result.returncode != 0:
            raise DfgetCommandError(
                "dfget command failed",
                returncode=result.returncode,
                stdout=_decode(result.stdout),
                stderr=_decode(result.stderr),
                argv=command,
            )
        return _extract_version(result.stdout, result.stderr)

    def health(self, timeout: Optional[float] = None) -> HealthResult:
        """Probe whether the dfget CLI is usable."""
        binary = self._resolve_binary()
        if not binary:
            return HealthResult(
                healthy=False, binary=None, version=None, message="dfget binary not found"
            )
        try:
            version_str = self.version(timeout=timeout)
        except DfgetNotFoundError as exc:
            return HealthResult(
                healthy=False, binary=binary, version=None, message=str(exc)
            )
        except DfgetError as exc:
            return HealthResult(
                healthy=False, binary=binary, version=None, message=str(exc)
            )
        return HealthResult(healthy=True, binary=binary, version=version_str, message="ok")

    async def aversion(self, timeout: Optional[float] = None) -> str:
        """Asyncio-native twin of :meth:`version`."""
        binary = self._ensure_binary()
        command = [binary, "--version"]

        async def _spawn() -> "asyncio.subprocess.Process":
            return await asyncio.create_subprocess_exec(
                *command, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
            )

        proc = await asyncio.wait_for(_spawn(), timeout=self._effective_timeout(timeout))
        communicate_result = await asyncio.wait_for(proc.communicate(), timeout=self._effective_timeout(timeout))
        stdout = _decode(communicate_result[0])
        stderr = _decode(communicate_result[1])
        if communicate_result[2].returncode != 0:
            raise DfgetCommandError(
                "dfget command failed",
                returncode=communicate_result[2].returncode,
                stdout=stdout,
                stderr=stderr,
                argv=list(command),
            )
        return _extract_version(stdout, stderr)

    async def ahealth(self, timeout: Optional[float] = None) -> HealthResult:
        """Asyncio-native twin of :meth:`health`."""
        binary = self._resolve_binary()
        if not binary:
            return HealthResult(
                healthy=False, binary=None, version=None, message="dfget binary not found"
            )
        try:
            version_str = await self.aversion(timeout=timeout)
        except DfgetNotFoundError as exc:
            return HealthResult(healthy=False, binary=binary, version=None, message=str(exc))
        except DfgetError as exc:
            return HealthResult(healthy=False, binary=binary, version=None, message=str(exc))
        return HealthResult(healthy=True, binary=binary, version=version_str, message="ok")
