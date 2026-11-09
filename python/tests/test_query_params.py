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

from dragonfly_sdk._query_params import (
    DEFAULT_FILTERED_QUERY_PARAMS,
    default_filtered_query_params,
)

EXPECTED = (
    "X-Amz-Algorithm",
    "X-Amz-Credential",
    "X-Amz-Date",
    "X-Amz-Expires",
    "X-Amz-SignedHeaders",
    "X-Amz-Signature",
    "X-Amz-Security-Token",
    "X-Amz-User-Agent",
    "X-Goog-Algorithm",
    "X-Goog-Credential",
    "X-Goog-Date",
    "X-Goog-Expires",
    "X-Goog-SignedHeaders",
    "X-Goog-Signature",
    "OSSAccessKeyId",
    "Expires",
    "Signature",
    "SecurityToken",
    "AccessKeyId",
    "X-Obs-Date",
    "X-Obs-Security-Token",
    "q-sign-algorithm",
    "q-ak",
    "q-sign-time",
    "q-key-time",
    "q-header-list",
    "q-url-param-list",
    "q-signature",
    "x-cos-security-token",
    "ns",
)


def test_matches_dfget_default_order() -> None:
    assert DEFAULT_FILTERED_QUERY_PARAMS == EXPECTED


def test_has_thirty_unique_params() -> None:
    assert len(DEFAULT_FILTERED_QUERY_PARAMS) == 30
    assert len(set(DEFAULT_FILTERED_QUERY_PARAMS)) == 30


def test_duplicates_across_protocols_appear_once() -> None:
    assert DEFAULT_FILTERED_QUERY_PARAMS.count("Signature") == 1
    assert DEFAULT_FILTERED_QUERY_PARAMS.count("Expires") == 1


def test_returns_a_fresh_list() -> None:
    first = default_filtered_query_params()
    second = default_filtered_query_params()
    assert first == second
    assert first is not second

    first.append("mutated")
    assert "mutated" not in default_filtered_query_params()
