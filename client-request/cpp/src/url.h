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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_URL_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_URL_H_

#include <string>
#include <utility>
#include <vector>

#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"

namespace dragonfly::client_request::internal {

/// A url canonical as the WHATWG url of the Rust client: its host lowercased,
/// its default port left out, its path resolved and escaped.
class Url {
 public:
  /// Parses a url, failing when it is not an absolute url.
  static absl::StatusOr<Url> Parse(absl::string_view url);

  /// Reports whether the url is an http or https url, which has a host.
  bool IsHttp() const { return is_http_; }

  /// Returns the canonical url.
  const std::string& ToString() const { return href_; }

 private:
  Url(std::string href, bool is_http)
      : href_(std::move(href)), is_http_(is_http) {}

  std::string href_;
  bool is_http_;
};

/// Filters and sorts the query params to canonicalize the url for the task
/// id, as the Rust client does.
absl::StatusOr<std::string> FilterQueryParams(
    absl::string_view url,
    const std::vector<std::string>& filtered_query_params);

/// Escapes the string for a url query, identical to Go's url.QueryEscape.
std::string QueryEscape(absl::string_view s);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_URL_H_
