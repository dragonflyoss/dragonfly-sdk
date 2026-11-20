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

#include "src/id_generator.h"

#include <openssl/sha.h>

#include "absl/strings/escaping.h"
#include "absl/strings/str_cat.h"
#include "src/digest.h"
#include "src/url.h"

namespace dragonfly::client_request::internal {
namespace {

std::string Sha256(absl::string_view data) {
  unsigned char digest[SHA256_DIGEST_LENGTH];
  SHA256(reinterpret_cast<const unsigned char*>(data.data()), data.size(),
         digest);
  return absl::BytesToHexString(absl::string_view(
      reinterpret_cast<const char*>(digest), SHA256_DIGEST_LENGTH));
}

}  // namespace

absl::StatusOr<std::string> TaskId(
    absl::string_view url, std::optional<std::uint64_t> piece_length,
    absl::string_view tag, absl::string_view application,
    const std::vector<std::string>& filtered_query_params,
    absl::string_view content_for_calculating_task_id,
    bool enable_task_id_based_blob_digest) {
  if (!content_for_calculating_task_id.empty()) {
    return Sha256(content_for_calculating_task_id);
  }

  // Share a blob or a manifest across registries by its digest.
  if (enable_task_id_based_blob_digest && IsBlobUrl(url)) {
    auto digest = ExtractDigestFromBlobUrl(url);
    if (!digest) {
      return absl::InternalError(
          absl::StrCat("failed to generate task id: invalid blob url: ", url));
    }

    return digest->encoded;
  }

  if (enable_task_id_based_blob_digest) {
    if (auto digest = ExtractDigestFromManifestUrl(url)) {
      return digest->encoded;
    }
  }

  // Canonicalize the url, identical to the scheduler's task id generation.
  auto canonical_url = FilterQueryParams(url, filtered_query_params);
  if (!canonical_url.ok()) {
    return absl::InternalError(absl::StrCat("failed to generate task id: ",
                                            canonical_url.status().message()));
  }

  return Sha256(absl::StrCat(*canonical_url, tag, application,
                             piece_length ? absl::StrCat(*piece_length) : "",
                             "STANDARD"));
}

}  // namespace dragonfly::client_request::internal
