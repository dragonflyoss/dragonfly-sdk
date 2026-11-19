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

"""Resolution of the dfdaemon Unix domain socket path."""

from __future__ import annotations

import os
import sys
from pathlib import Path
from typing import Union

from ._errors import ValidationError

LINUX_DEFAULT_ENDPOINT = Path("/var/run/dragonfly/dfdaemon.sock")
MACOS_DEFAULT_ENDPOINT = Path.home() / ".dragonfly" / "dfdaemon.sock"

EndpointLike = Union[str, "os.PathLike[str]"]


def default_endpoint() -> Path:
    """Return the socket path dfdaemon listens on for the current OS."""
    if sys.platform == "darwin":
        return MACOS_DEFAULT_ENDPOINT

    if sys.platform.startswith("linux"):
        return LINUX_DEFAULT_ENDPOINT

    raise ValidationError(
        f"cannot determine the default dfdaemon socket on {sys.platform!r}; "
        "pass endpoint= explicitly"
    )


def resolve_endpoint(endpoint: EndpointLike | None = None) -> Path:
    """Resolve a user-supplied endpoint to an absolute socket path."""
    if endpoint is None:
        return default_endpoint()

    if not isinstance(endpoint, (str, os.PathLike)):
        raise ValidationError("endpoint must be a filesystem path")

    raw = os.fspath(endpoint)
    if not isinstance(raw, str):
        raise ValidationError("endpoint must be a filesystem path")

    if raw.startswith("unix:"):
        raise ValidationError(f"endpoint must be a socket path, not a gRPC target: {raw!r}")

    path = Path(raw).expanduser()
    if not path.is_absolute():
        raise ValidationError(f"dfdaemon socket path must be absolute: {raw!r}")

    return path


def channel_target(path: Path) -> str:
    """Return the gRPC target for a Unix domain socket path."""
    return f"unix:{path}"
