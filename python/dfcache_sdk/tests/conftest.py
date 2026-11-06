"""pytest fixtures for dragonfly_dfcache tests.

All tests mock the underlying ``dfcache`` subprocess (both the sync
``subprocess.run`` and the asyncio subprocess APIs), so they pass
without a running Dragonfly cluster or even a real dfcache binary.
"""

from __future__ import annotations

import asyncio
import subprocess
import sys
from pathlib import Path

import pytest

# Make the ``src`` layout importable without installing the package.
SRC_DIR = Path(__file__).resolve().parent.parent / "src"
if str(SRC_DIR) not in sys.path:
    sys.path.insert(0, str(SRC_DIR))

from dragonfly_dfcache import DfcacheClient, DfcacheConfig  # noqa: E402

# Environment variables read by DfcacheConfig.__post_init__.
_CONFIG_ENV_VARS = (
    "DFCACHE_ENDPOINT",
    "DFCACHE_TLS",
    "DFCACHE_TLS_CA_CERT",
    "DFCACHE_TIMEOUT",
    "DFCACHE_BINARY",
)


@pytest.fixture
def clean_env(monkeypatch):
    """Remove all ``DFCACHE_*`` environment variables for env isolation.

    Keeps config-resolution tests deterministic regardless of the host
    machine or CI environment.
    """
    for var in _CONFIG_ENV_VARS:
        monkeypatch.delenv(var, raising=False)


@pytest.fixture
def client(clean_env):
    """Build a client with a fixed bare-name binary to avoid PATH surprises."""
    return DfcacheClient(DfcacheConfig(binary_path="dfcache"))


class _FakeCompletedProcess:
    """Stand-in for :class:`subprocess.CompletedProcess`."""

    def __init__(self, returncode, stdout, stderr):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


class MockRun:
    """Callable monkeypatch target for :func:`subprocess.run`.

    Configure per test::

        mock_run.result = (0, '{"size": 1}', "")
        mock_run.error = subprocess.TimeoutExpired("dfcache", 1)

    The instance records every argv (``commands``) and every effective
    ``timeout`` kwarg (``timeouts``) so tests can assert the flags and
    timeout passed to dfcache.
    """

    result = (0, "", "")
    error = None

    def __init__(self):
        self.commands = []
        self.timeouts = []

    def __call__(self, cmd, **kwargs):
        self.commands.append(list(cmd))
        self.timeouts.append(kwargs.get("timeout"))
        if self.error is not None:
            raise self.error
        code, out, err = self.result
        return _FakeCompletedProcess(code, out, err)


@pytest.fixture
def mock_run(monkeypatch):
    """Monkeypatch :func:`subprocess.run` with a :class:`MockRun`."""
    mock = MockRun()
    monkeypatch.setattr(subprocess, "run", mock)
    return mock


class _FakeAsyncProcess:
    """Stand-in for :class:`asyncio.subprocess.Process`."""

    def __init__(self, returncode, stdout, stderr):
        self._result = (returncode, stdout, stderr)
        self.returncode = returncode

    async def communicate(self):
        if isinstance(self._delay, float) and self._delay > 0:
            await asyncio.sleep(self._delay)
        return self._result[1], self._result[2]

    _delay = 0.0


class MockAsyncProc:
    """Callable monkeypatch target for :func:`asyncio.create_subprocess_exec`.

    Configure per test::

        mock_asyncproc.result = (0, '{"size": 1}', "")
        mock_asyncproc.error = FileNotFoundError()
        mock_asyncproc.delay = 1.0   # communicate() hangs this long
    """

    result = (0, "", "")
    error = None
    # Seconds communicate() blocks before returning; use with a small
    # client timeout to trigger DfcacheTimeoutError.
    delay = 0.0

    def __init__(self):
        self.commands = []

    async def __call__(self, *cmd, **kwargs):
        self.commands.append(list(cmd))
        if self.error is not None:
            raise self.error
        proc = _FakeAsyncProcess(*self.result)
        proc._delay = self.delay
        return proc


@pytest.fixture
def mock_asyncproc(monkeypatch):
    """Monkeypatch :func:`asyncio.create_subprocess_exec` with a :class:`MockAsyncProc`."""
    mock = MockAsyncProc()
    monkeypatch.setattr(asyncio, "create_subprocess_exec", mock)
    return mock
