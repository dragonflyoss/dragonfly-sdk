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

"""Parsing of request headers, mirroring dfget's ``--header`` handling."""

from __future__ import annotations

from collections.abc import Iterable, Mapping
from typing import Union

from ._errors import ValidationError

HeadersLike = Union[Mapping[str, str], Iterable[str], None]


def parse_headers(headers: HeadersLike) -> list[tuple[str, str]]:
    """Normalize headers to a list of ``(name, value)`` pairs.

    Accepts either a mapping or an iterable of ``"Name: value"`` strings. Lines
    without a colon are dropped and duplicate names keep the last value, the
    same way dfget builds the ``request_header`` map.
    """
    if headers is None:
        return []

    parsed: dict[str, str] = {}

    if isinstance(headers, Mapping):
        for name, value in headers.items():
            if not isinstance(name, str) or not isinstance(value, str):
                raise ValidationError("header names and values must be strings")

            name = name.strip()
            if not name:
                continue

            parsed[name] = value.strip()
    else:
        if isinstance(headers, (str, bytes)):
            raise ValidationError(
                "headers must be a mapping or an iterable of 'Name: value' strings"
            )

        for line in headers:
            if not isinstance(line, str):
                raise ValidationError("each header must be a string")

            name, separator, value = line.partition(":")
            if not separator:
                continue

            name = name.strip()
            if not name:
                continue

            parsed[name] = value.strip()

    return list(parsed.items())
