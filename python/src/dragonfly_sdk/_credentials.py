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

"""Credentials for the download backends supported by dfdaemon."""

from __future__ import annotations

from dataclasses import dataclass
from typing import Union


@dataclass(frozen=True)
class ObjectStorageCredentials:
    """Credentials for S3-compatible object storage.

    Used for URLs whose scheme is ``s3``, ``gs``, ``abs``, ``oss``, ``obs`` or
    ``cos``.
    """

    region: str = ""
    endpoint: str = ""
    access_key_id: str = ""
    access_key_secret: str = ""
    session_token: str = ""
    credential_path: str = ""
    predefined_acl: str = "publicRead"
    security_token: str = ""
    insecure_skip_verify: bool = False


@dataclass(frozen=True)
class HuggingFaceCredentials:
    """Credentials for ``hf://`` URLs."""

    token: str = ""
    revision: str = "main"
    base_url: str = ""


@dataclass(frozen=True)
class ModelScopeCredentials:
    """Credentials for ``modelscope://`` URLs."""

    token: str = ""
    revision: str = "master"
    base_url: str = ""


@dataclass(frozen=True)
class OpenCSGCredentials:
    """Credentials for ``opencsg://`` URLs."""

    token: str = ""
    revision: str = "main"
    base_url: str = ""


Credentials = Union[
    ObjectStorageCredentials,
    HuggingFaceCredentials,
    ModelScopeCredentials,
    OpenCSGCredentials,
]
