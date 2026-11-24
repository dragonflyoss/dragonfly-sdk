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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_RANGE_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_RANGE_H_

#include <cstdint>
#include <optional>
#include <string>

#include "absl/strings/string_view.h"

namespace dragonfly::client_request::internal {

/// The byte range of a request or a response.
struct HttpRange {
  /// The position of the first byte.
  std::int64_t start = 0;

  /// The number of bytes.
  std::int64_t length = 0;

  /// Returns the value of the Range header, "bytes=<start>-<end>".
  std::string ToString() const;
};

/// The byte range a response carries and the size of the whole resource.
struct ContentRange {
  /// The byte range of the response.
  HttpRange range;

  /// The size of the whole resource.
  std::int64_t size = 0;
};

/// Reports whether the content ranges are equal.
bool operator==(const ContentRange& a, const ContentRange& b);

/// Reports whether the content ranges differ.
bool operator!=(const ContentRange& a, const ContentRange& b);

/// Parses a Content-Range header, nullopt when it is invalid, unsatisfied or
/// of unknown size.
std::optional<ContentRange> ParseContentRange(absl::string_view value);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_RANGE_H_
