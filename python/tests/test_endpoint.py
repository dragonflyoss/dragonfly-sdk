# Copyright 2026 The Dragonfly Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from __future__ import annotations

import sys
from pathlib import Path

import pytest

from dragonfly_sdk import ValidationError
from dragonfly_sdk._endpoint import (
    LINUX_DEFAULT_ENDPOINT,
    channel_target,
    default_endpoint,
    resolve_endpoint,
)


def test_linux_default(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "platform", "linux")
    assert default_endpoint() == Path("/var/run/dragonfly/dfdaemon.sock")
    assert default_endpoint() == LINUX_DEFAULT_ENDPOINT


def test_macos_default(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "platform", "darwin")
    assert default_endpoint() == Path.home() / ".dragonfly" / "dfdaemon.sock"


def test_unsupported_platform(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "platform", "win32")
    with pytest.raises(ValidationError, match="endpoint="):
        default_endpoint()


def test_resolve_none_uses_default(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(sys, "platform", "linux")
    assert resolve_endpoint() == LINUX_DEFAULT_ENDPOINT


def test_resolve_expands_home() -> None:
    assert resolve_endpoint("~/.dragonfly/dfdaemon.sock") == (
        Path.home() / ".dragonfly" / "dfdaemon.sock"
    )


def test_resolve_accepts_path() -> None:
    assert resolve_endpoint(Path("/tmp/dfdaemon.sock")) == Path("/tmp/dfdaemon.sock")


def test_resolve_rejects_relative() -> None:
    with pytest.raises(ValidationError, match="absolute"):
        resolve_endpoint("dfdaemon.sock")


def test_resolve_rejects_grpc_target() -> None:
    with pytest.raises(ValidationError, match="gRPC target"):
        resolve_endpoint("unix:/var/run/dragonfly/dfdaemon.sock")


def test_resolve_rejects_non_path() -> None:
    with pytest.raises(ValidationError):
        resolve_endpoint(123)  # type: ignore[arg-type]


def test_channel_target() -> None:
    assert channel_target(Path("/tmp/dfdaemon.sock")) == "unix:/tmp/dfdaemon.sock"
