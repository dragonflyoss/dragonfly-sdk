#     Copyright 2026 The Dragonfly Authors
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

"""Request library for the Dragonfly client. It is the Python implementation
of the Rust crate dragonfly-client-request and the Go module
d7y.io/dragonfly-sdk/client-request/go, and generates identical task ids.
"""

from .errors import (
    BackendError,
    DfdaemonError,
    InternalError,
    InvalidArgumentError,
    ProxyError,
    RequestError,
    RequestTimeoutError,
    ResponseError,
)
from .id_generator import (
    CONTAINERD_FILTERED_QUERY_PARAMS,
    COS_FILTERED_QUERY_PARAMS,
    DEFAULT_FILTERED_QUERY_PARAMS,
    GCS_FILTERED_QUERY_PARAMS,
    OBS_FILTERED_QUERY_PARAMS,
    OSS_FILTERED_QUERY_PARAMS,
    S3_FILTERED_QUERY_PARAMS,
    IDGenerator,
    task_id,
    task_id_by_blob_digest,
    task_id_by_content,
    task_id_by_manifest_digest,
    task_id_by_url,
)

__all__ = [
    "CONTAINERD_FILTERED_QUERY_PARAMS",
    "COS_FILTERED_QUERY_PARAMS",
    "DEFAULT_FILTERED_QUERY_PARAMS",
    "GCS_FILTERED_QUERY_PARAMS",
    "OBS_FILTERED_QUERY_PARAMS",
    "OSS_FILTERED_QUERY_PARAMS",
    "S3_FILTERED_QUERY_PARAMS",
    "BackendError",
    "DfdaemonError",
    "IDGenerator",
    "InternalError",
    "InvalidArgumentError",
    "ProxyError",
    "RequestError",
    "RequestTimeoutError",
    "ResponseError",
    "task_id",
    "task_id_by_blob_digest",
    "task_id_by_content",
    "task_id_by_manifest_digest",
    "task_id_by_url",
]
