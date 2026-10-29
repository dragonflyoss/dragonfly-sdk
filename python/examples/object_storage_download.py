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

"""Download an object from S3-compatible storage."""

from __future__ import annotations

from dragonfly_sdk import Client, ObjectStorageCredentials

ACCESS_KEY_ID = "your-access-key-id"
ACCESS_KEY_SECRET = "your-access-key-secret"


def main() -> None:
    credentials = ObjectStorageCredentials(
        region="us-east-1",
        access_key_id=ACCESS_KEY_ID,
        access_key_secret=ACCESS_KEY_SECRET,
    )

    with Client() as client:
        result = client.download(
            "s3://my-bucket/datasets/train.parquet",
            "./train.parquet",
            credentials=credentials,
        )

    print(f"downloaded {result.content_length} bytes to {result.output_path}")


if __name__ == "__main__":
    main()
