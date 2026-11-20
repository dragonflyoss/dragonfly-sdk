/*
 *     Copyright 2026 The Dragonfly Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *      http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_DIGEST_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_DIGEST_H_

#include <optional>
#include <string>

#include "absl/strings/string_view.h"

namespace dragonfly::client_request::internal {

/// A digest value with its algorithm.
struct Digest {
  /// The algorithm of the digest: "crc32", "sha256" or "sha512".
  std::string algorithm;

  /// The encoded digest value.
  std::string encoded;
};

/// Reports whether the url is an OCI blob url, such as
/// "http(s)://<registry>/v2/<repository>/blobs/<digest>".
bool IsBlobUrl(absl::string_view url);

/// Reports whether the url is an OCI manifest url referenced by a digest, not
/// by a tag.
bool IsManifestDigestUrl(absl::string_view url);

/// Extracts the digest from an OCI blob url, nullopt when it has none.
std::optional<Digest> ExtractDigestFromBlobUrl(absl::string_view url);

/// Extracts the digest from an OCI manifest url, nullopt when it has none.
std::optional<Digest> ExtractDigestFromManifestUrl(absl::string_view url);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_DIGEST_H_
