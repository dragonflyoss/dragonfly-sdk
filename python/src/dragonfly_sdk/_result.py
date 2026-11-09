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

"""Result of a completed download."""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path


@dataclass(frozen=True)
class DownloadResult:
    """Summary of a download that dfdaemon reported as complete."""

    url: str
    output_path: Path
    task_id: str
    peer_id: str
    host_id: str
    content_length: int
    total_pieces: int
    completed_pieces: int
    downloaded_bytes: int
    response_headers: Mapping[str, str]
    elapsed: timedelta
