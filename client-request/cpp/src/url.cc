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

#include "src/url.h"

#include <ada.h>

#include <algorithm>
#include <string_view>

#include "absl/strings/ascii.h"
#include "absl/strings/match.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_join.h"
#include "absl/strings/str_split.h"

namespace dragonfly::client_request::internal {
namespace {

// Parses the url as a WHATWG url, the parser of the Rust client.
absl::StatusOr<ada::url_aggregator> ParseUrl(absl::string_view url) {
  auto parsed =
      ada::parse<ada::url_aggregator>(std::string_view(url.data(), url.size()));
  if (!parsed) {
    return absl::InvalidArgumentError(absl::StrCat("invalid url: ", url));
  }

  return *parsed;
}

// Reports whether the url is written without a path after its authority,
// splitting the authority as Go's url does.
bool HasEmptyPath(absl::string_view url) {
  const auto authority = url.find("://");
  if (authority == absl::string_view::npos) {
    return false;
  }

  url.remove_prefix(authority + 3);
  return !absl::StrContains(url.substr(0, url.find_first_of("?#")), '/');
}

// Returns the value of a hex digit, -1 when it is not one.
int HexValue(char c) {
  return absl::ascii_isdigit(c)    ? c - '0'
         : absl::ascii_isxdigit(c) ? absl::ascii_tolower(c) - 'a' + 10
                                   : -1;
}

// Replaces every invalid UTF-8 sequence with U+FFFD, identical to Rust's
// String::from_utf8_lossy.
std::string ToValidUtf8(absl::string_view bytes) {
  std::string valid;
  for (std::size_t i = 0; i < bytes.size();) {
    // The lead byte bounds the second one, leaving out the overlong forms,
    // the surrogates and the code points past U+10FFFF.
    const unsigned char lead = bytes[i];
    const std::size_t length = lead < 0x80   ? 1
                               : lead < 0xc2 ? 0
                               : lead < 0xe0 ? 2
                               : lead < 0xf0 ? 3
                               : lead < 0xf5 ? 4
                                             : 0;
    const unsigned char low = lead == 0xe0 ? 0xa0 : lead == 0xf0 ? 0x90 : 0x80;
    const unsigned char high = lead == 0xed ? 0x9f : lead == 0xf4 ? 0x8f : 0xbf;

    std::size_t n = 1;
    for (; n < length && i + n < bytes.size(); ++n) {
      const unsigned char c = bytes[i + n];
      if (c < (n == 1 ? low : 0x80) || c > (n == 1 ? high : 0xbf)) {
        break;
      }
    }

    if (n == length) {
      valid.append(bytes.data() + i, n);
    } else {
      valid.append("\xef\xbf\xbd");
    }
    i += n;
  }

  return valid;
}

// Unescapes a key or a value of a query as the form_urlencoded parser of the
// Rust client: '+' is a space and an invalid escape is kept.
std::string QueryUnescape(absl::string_view s) {
  std::string bytes;
  for (std::size_t i = 0; i < s.size(); ++i) {
    if (s[i] == '%' && i + 2 < s.size() && HexValue(s[i + 1]) >= 0 &&
        HexValue(s[i + 2]) >= 0) {
      bytes.push_back(
          static_cast<char>(HexValue(s[i + 1]) * 16 + HexValue(s[i + 2])));
      i += 2;
    } else {
      bytes.push_back(s[i] == '+' ? ' ' : s[i]);
    }
  }

  return ToValidUtf8(bytes);
}

// Returns the query without the filtered params, the others sorted by key
// with the values of a key in their order, identical to Go's
// url.Values.Encode.
std::string FilterQuery(absl::string_view query,
                        const std::vector<std::string>& filtered_query_params) {
  std::vector<std::pair<std::string, std::string>> params;
  for (absl::string_view param :
       absl::StrSplit(query, '&', absl::SkipEmpty())) {
    if (absl::StrContains(param, ';')) {
      continue;
    }

    const std::pair<absl::string_view, absl::string_view> key_value =
        absl::StrSplit(param, absl::MaxSplits('=', 1));
    std::string key = QueryUnescape(key_value.first);
    if (std::find(filtered_query_params.begin(), filtered_query_params.end(),
                  key) == filtered_query_params.end()) {
      params.emplace_back(std::move(key), QueryUnescape(key_value.second));
    }
  }

  std::stable_sort(
      params.begin(), params.end(),
      [](const auto& a, const auto& b) { return a.first < b.first; });
  return absl::StrJoin(params, "&", [](std::string* out, const auto& param) {
    absl::StrAppend(out, QueryEscape(param.first), "=",
                    QueryEscape(param.second));
  });
}

}  // namespace

absl::StatusOr<Url> Url::Parse(absl::string_view url) {
  auto parsed = ParseUrl(url);
  if (!parsed.ok()) {
    return parsed.status();
  }

  const std::string_view protocol = parsed->get_protocol();
  return Url(std::string(parsed->get_href()),
             protocol == "http:" || protocol == "https:");
}

absl::StatusOr<std::string> FilterQueryParams(
    absl::string_view url,
    const std::vector<std::string>& filtered_query_params) {
  if (filtered_query_params.empty()) {
    return std::string(url);
  }

  auto parsed = ParseUrl(url);
  if (!parsed.ok()) {
    return parsed.status();
  }

  // A query without any param left is left out with its "?".
  std::string_view search = parsed->get_search();
  search.remove_prefix(std::min<std::size_t>(search.size(), 1));
  parsed->set_search(FilterQuery(
      absl::string_view(search.data(), search.size()), filtered_query_params));

  // Leave out the path a WHATWG url gives a url written without one, as Go's
  // url does.
  std::string canonical(parsed->get_href());
  if (HasEmptyPath(url)) {
    const ada::url_components& components = parsed->get_components();
    const auto path_end = std::min<std::size_t>(
        {components.search_start, components.hash_start, canonical.size()});
    canonical.erase(components.pathname_start,
                    path_end - components.pathname_start);
  }

  return canonical;
}

std::string QueryEscape(absl::string_view s) {
  constexpr char kHex[] = "0123456789ABCDEF";
  std::string escaped;
  for (unsigned char c : s) {
    if (absl::ascii_isalnum(c) || c == '-' || c == '.' || c == '_' ||
        c == '~') {
      escaped.push_back(static_cast<char>(c));
    } else if (c == ' ') {
      escaped.push_back('+');
    } else {
      escaped.append({'%', kHex[c >> 4], kHex[c & 15]});
    }
  }

  return escaped;
}

}  // namespace dragonfly::client_request::internal
