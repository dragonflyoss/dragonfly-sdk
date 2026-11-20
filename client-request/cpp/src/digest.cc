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

#include "src/digest.h"

#include <re2/re2.h>

namespace dragonfly::client_request::internal {
namespace {

// The regexes of the Rust client for the OCI blob urls, such as
// "http(s)://<registry>/v2/<repository>/blobs/<digest>", and manifest urls.
struct UrlRegexes {
  const RE2 blob{R"(^(.*)://(.*)/v2/(.*)/blobs/([^?]+)(?:\?.*)?$)",
                 RE2::Latin1};
  const RE2 manifest{R"(^(.*)://(.*)/v2/(.*)/manifests/([^?]+)(?:\?.*)?$)",
                     RE2::Latin1};
};

const UrlRegexes& Regexes() {
  static const auto* const regexes = new UrlRegexes();
  return *regexes;
}

// Returns the digest part of the url the regex matches, empty otherwise.
absl::string_view DigestPart(absl::string_view url, const RE2& regex) {
  re2::StringPiece digest;
  if (!RE2::FullMatch(re2::StringPiece(url.data(), url.size()), regex, nullptr,
                      nullptr, nullptr, &digest)) {
    return {};
  }

  return {digest.data(), digest.size()};
}

// Parses a digest such as "sha256:<64 characters>", nullopt when the algorithm
// is not supported or the encoded value has the wrong length.
std::optional<Digest> ParseDigest(absl::string_view digest) {
  const auto separator = digest.find(':');
  if (separator == absl::string_view::npos) {
    return std::nullopt;
  }

  const absl::string_view algorithm = digest.substr(0, separator);
  const absl::string_view encoded = digest.substr(separator + 1);
  if ((algorithm == "crc32" && !encoded.empty()) ||
      (algorithm == "sha256" && encoded.size() == 64) ||
      (algorithm == "sha512" && encoded.size() == 128)) {
    return Digest{std::string(algorithm), std::string(encoded)};
  }

  return std::nullopt;
}

}  // namespace

bool IsBlobUrl(absl::string_view url) {
  return !DigestPart(url, Regexes().blob).empty();
}

bool IsManifestDigestUrl(absl::string_view url) {
  return ExtractDigestFromManifestUrl(url).has_value();
}

std::optional<Digest> ExtractDigestFromBlobUrl(absl::string_view url) {
  return ParseDigest(DigestPart(url, Regexes().blob));
}

std::optional<Digest> ExtractDigestFromManifestUrl(absl::string_view url) {
  return ParseDigest(DigestPart(url, Regexes().manifest));
}

}  // namespace dragonfly::client_request::internal
