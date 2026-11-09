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

"""Default filtered query parameters used to generate the task id.

This mirrors ``default_proxy_rule_filtered_query_params`` from
``dragonfly-client-util/src/http/query_params.rs``: the ordered union of the
S3, GCS, OSS, OBS, COS and containerd lists with duplicates removed.
"""

from __future__ import annotations

DEFAULT_FILTERED_QUERY_PARAMS: tuple[str, ...] = (
    # S3
    "X-Amz-Algorithm",
    "X-Amz-Credential",
    "X-Amz-Date",
    "X-Amz-Expires",
    "X-Amz-SignedHeaders",
    "X-Amz-Signature",
    "X-Amz-Security-Token",
    "X-Amz-User-Agent",
    # GCS
    "X-Goog-Algorithm",
    "X-Goog-Credential",
    "X-Goog-Date",
    "X-Goog-Expires",
    "X-Goog-SignedHeaders",
    "X-Goog-Signature",
    # OSS
    "OSSAccessKeyId",
    "Expires",
    "Signature",
    "SecurityToken",
    # OBS
    "AccessKeyId",
    "X-Obs-Date",
    "X-Obs-Security-Token",
    # COS
    "q-sign-algorithm",
    "q-ak",
    "q-sign-time",
    "q-key-time",
    "q-header-list",
    "q-url-param-list",
    "q-signature",
    "x-cos-security-token",
    # containerd
    "ns",
)


def default_filtered_query_params() -> list[str]:
    """Return a fresh copy of the default filtered query parameter list."""
    return list(DEFAULT_FILTERED_QUERY_PARAMS)
