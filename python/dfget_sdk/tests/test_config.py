"""Configuration resolution tests: precedence and env passthrough."""

from __future__ import annotations

import dataclasses
import os
from pathlib import Path

import pytest

from conftest import make_fake_path_binary
from dragonfly_dfget.config import (
    DEFAULT_TIMEOUT,
    DFGET_BIN_ENV,
    DFDAEMON_ENDPOINT_ENV,
    DfgetConfig,
)
from dragonfly_dfget.errors import DfgetError


def test_defaults() -> None:
    config = DfgetConfig()
    assert config.binary is None
    assert config.endpoint is None
    assert config.default_timeout == DEFAULT_TIMEOUT == "2h"
    assert config.log_level is None


def test_config_is_frozen_and_replace_friendly() -> None:
    config = DfgetConfig(binary="dfget", default_timeout="30m")
    with pytest.raises(dataclasses.FrozenInstanceError):
        config.binary = "other"  # type: ignore[misc]
    derived = config.replace(default_timeout="10m")
    assert derived.default_timeout == "10m"
    assert derived.binary == "dfget"
    # original untouched
    assert config.default_timeout == "30m"


def test_invalid_log_level_rejected() -> None:
    with pytest.raises(ValueError):
        DfgetConfig(log_level="verbose")


@pytest.mark.parametrize("level", ["trace", "debug", "info", "warn", "error"])
def test_valid_log_levels(level: str) -> None:
    assert DfgetConfig(log_level=level).log_level == level


def test_binary_precedence_explicit_beats_env(
    tmp_path: Path, path_env
) -> None:
    env = dict(path_env)
    env[DFGET_BIN_ENV] = str(tmp_path / "from-env")
    config = DfgetConfig(binary=str(tmp_path / "explicit"))
    assert config.resolve_binary(env) == str(tmp_path / "explicit")


def test_binary_precedence_env_beats_path(tmp_path: Path, path_env) -> None:
    env = dict(path_env)
    env[DFGET_BIN_ENV] = str(tmp_path / "from-env")
    config = DfgetConfig()
    assert config.resolve_binary(env) == str(tmp_path / "from-env")


def test_binary_precedence_path_lookup(tmp_path: Path, empty_env_dir: Path) -> None:
    created = make_fake_path_binary(empty_env_dir)
    env = {"PATH": str(empty_env_dir)}
    config = DfgetConfig()
    found = config.resolve_binary(env)
    assert found is not None
    # shutil.which may return a PATHEXT-cased name on Windows (dfget.BAT)
    assert os.path.normcase(found) == os.path.normcase(created)


def test_binary_not_found_returns_none(path_env) -> None:
    assert DfgetConfig().resolve_binary(dict(path_env)) is None


def test_endpoint_explicit_beats_env() -> None:
    env = {DFDAEMON_ENDPOINT_ENV: "/var/run/env.sock"}
    config = DfgetConfig(endpoint="/var/run/explicit.sock")
    assert config.resolve_endpoint(env) == "/var/run/explicit.sock"


def test_endpoint_env_passthrough(path_env) -> None:
    env = dict(path_env)
    env[DFDAEMON_ENDPOINT_ENV] = "/var/run/dfdaemon.sock"
    assert DfgetConfig().resolve_endpoint(env) == "/var/run/dfdaemon.sock"


def test_endpoint_absent_is_none(path_env) -> None:
    assert DfgetConfig().resolve_endpoint(dict(path_env)) is None


def test_from_env_reads_timeout_and_log_level() -> None:
    env = {"DFGET_TIMEOUT": "45s", "DFGET_LOG_LEVEL": "debug"}
    config = DfgetConfig.from_env(env)
    assert config.default_timeout == "45s"
    assert config.log_level == "debug"
    # binary/endpoint are resolved lazily, not baked in
    assert config.binary is None
    assert config.endpoint is None


def test_from_env_defaults(path_env) -> None:
    config = DfgetConfig.from_env(dict(path_env))
    assert config.default_timeout == "2h"
    assert config.log_level is None


def test_config_inherits_dfget_error_hierarchy() -> None:
    # sanity: config module does not define its own exception types
    assert not issubclass(DfgetConfig, BaseException)
    assert issubclass(DfgetError, Exception)
