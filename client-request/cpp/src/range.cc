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

#include "src/range.h"

#include <re2/re2.h>

#include "absl/strings/str_cat.h"

namespace dragonfly::client_request::internal {

std::string HttpRange::ToString() const {
  return absl::StrCat("bytes=", start, "-", start + length - 1);
}

bool operator==(const ContentRange& a, const ContentRange& b) {
  return a.range.start == b.range.start && a.range.length == b.range.length &&
         a.size == b.size;
}

bool operator!=(const ContentRange& a, const ContentRange& b) {
  return !(a == b);
}

std::optional<ContentRange> ParseContentRange(absl::string_view value) {
  // Match "bytes <start>-<end>/<size>", as per RFC 9110, each a number of
  // digits alone that fits in an int64.
  static const auto* const regex = new RE2(R"(bytes (\d+)-(\d+)/(\d+))");
  std::int64_t start = 0;
  std::int64_t end = 0;
  std::int64_t size = 0;
  if (!RE2::FullMatch(re2::StringPiece(value.data(), value.size()), *regex,
                      &start, &end, &size) ||
      start > end || end >= size) {
    return std::nullopt;
  }

  return ContentRange{{start, end - start + 1}, size};
}

}  // namespace dragonfly::client_request::internal
