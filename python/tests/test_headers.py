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

import pytest

from dragonfly_sdk import ValidationError
from dragonfly_sdk._headers import parse_headers


def test_none_returns_empty() -> None:
    assert parse_headers(None) == []


def test_mapping() -> None:
    assert parse_headers({"Accept": "text/plain", "Host": "localhost:8080"}) == [
        ("Accept", "text/plain"),
        ("Host", "localhost:8080"),
    ]


def test_lines_split_on_first_colon() -> None:
    assert parse_headers(["Content-Type: application/json", "Host:localhost:8080"]) == [
        ("Content-Type", "application/json"),
        ("Host", "localhost:8080"),
    ]


def test_values_and_names_are_trimmed() -> None:
    assert parse_headers(["  Accept :  text/plain  "]) == [("Accept", "text/plain")]
    assert parse_headers({"  Accept  ": "  text/plain  "}) == [("Accept", "text/plain")]


def test_malformed_lines_are_dropped() -> None:
    assert parse_headers(["invalid header", "", ": value"]) == []


def test_duplicates_keep_last_value() -> None:
    assert parse_headers(["X-Test: 1", "X-Test: 2"]) == [("X-Test", "2")]


def test_empty_mapping_key_is_dropped() -> None:
    assert parse_headers({"": "value", "X-Test": "1"}) == [("X-Test", "1")]


def test_bare_string_is_rejected() -> None:
    with pytest.raises(ValidationError):
        parse_headers("X-Test: 1")


def test_non_string_header_line_is_rejected() -> None:
    with pytest.raises(ValidationError):
        parse_headers([1])  # type: ignore[list-item]


def test_non_string_mapping_value_is_rejected() -> None:
    with pytest.raises(ValidationError):
        parse_headers({"X-Test": 1})  # type: ignore[dict-item]
