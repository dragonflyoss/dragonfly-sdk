"""Shared fixtures/helpers for the dfget SDK test-suite.

All subprocess calls are mocked - no real dfget binary, cluster, or network
access is required to run these tests.
"""

from __future__ import annotations

import os
import stat
import sys
from pathlib import Path
from typing import Mapping

import pytest

# Make ``src/`` importable even when the package is not installed (e.g. plain
# ``python -m pytest`` without uv/pip install).
SRC_DIR = Path(__file__).resolve().parents[1] / "src"
if str(SRC_DIR) not in sys.path:
    sys.path.insert(0, str(SRC_DIR))


def make_fake_path_binary(directory: Path) -> str:
    """Create an executable ``dfget`` entry in ``directory`` for PATH lookups.

    Returns the created file path.  On Windows a ``.bat`` shim is created so
    ``shutil.which("dfget", path=...)`` finds it via ``PATHEXT``; elsewhere a
    POSIX executable stub with a shebang is used.
    """
    if os.name == "nt":
        path = directory / "dfget.bat"
        path.write_text("@echo off\r\necho dfget 0.0.0-fake\r\n")
    else:
        path = directory / "dfget"
        path.write_text("#!/bin/sh\necho dfget 0.0.0-fake\n")
        path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)
    return str(path)


@pytest.fixture
def empty_env_dir(tmp_path: Path) -> Path:
    """A directory guaranteed NOT to contain a dfget binary (for PATH=...)."""
    empty = tmp_path / "empty-bin"
    empty.mkdir(exist_ok=True)
    return empty


@pytest.fixture
def path_env(empty_env_dir: Path) -> Mapping[str, str]:
    """Environment mapping whose PATH contains no dfget binary."""
    return {"PATH": str(empty_env_dir)}
