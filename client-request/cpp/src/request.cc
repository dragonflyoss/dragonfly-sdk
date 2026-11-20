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

#include "dragonfly/client_request/request.h"

#include <algorithm>
#include <iterator>

#include "absl/strings/ascii.h"
#include "absl/strings/escaping.h"
#include "absl/strings/match.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_join.h"
#include "absl/strings/str_split.h"
#include "src/request_internal.h"
#include "src/url.h"

namespace dragonfly::client_request {

const std::vector<std::string>& DefaultFilteredQueryParams() {
  static const auto* const params = new std::vector<std::string>{
      "X-Amz-Algorithm",
      "X-Amz-Credential",
      "X-Amz-Date",
      "X-Amz-Expires",
      "X-Amz-SignedHeaders",
      "X-Amz-Signature",
      "X-Amz-Security-Token",
      "X-Amz-User-Agent",
      "X-Goog-Algorithm",
      "X-Goog-Credential",
      "X-Goog-Date",
      "X-Goog-Expires",
      "X-Goog-SignedHeaders",
      "X-Goog-Signature",
      "OSSAccessKeyId",
      "Expires",
      "Signature",
      "SecurityToken",
      "AccessKeyId",
      "X-Obs-Date",
      "X-Obs-Security-Token",
      "q-sign-algorithm",
      "q-ak",
      "q-sign-time",
      "q-key-time",
      "q-header-list",
      "q-url-param-list",
      "q-signature",
      "x-cos-security-token",
      "ns",
  };

  return *params;
}

namespace internal {
namespace {

absl::Status ValidateReplicas(int replicas) {
  return replicas > 0 ? absl::OkStatus()
                      : absl::InvalidArgumentError("replicas must be positive");
}

absl::Status ValidateConcurrentPeerCount(int concurrent_peer_count) {
  return concurrent_peer_count > 0
             ? absl::OkStatus()
             : absl::InvalidArgumentError(
                   "concurrent peer count must be positive");
}

absl::Status ValidatePlatform(const std::optional<std::string>& platform) {
  return platform ? NormalizePlatform(*platform).status() : absl::OkStatus();
}

}  // namespace

absl::Status Validate(const GetRequest& request) {
  if (auto status = ValidateReplicas(request.replicas); !status.ok()) {
    return status;
  }

  // Only http and https urls with a host go through the seed peer proxies.
  auto url = Url::Parse(request.url);
  if (!url.ok()) {
    return url.status();
  }

  if (!url->IsHttp()) {
    return absl::InvalidArgumentError(absl::StrCat(
        "url \"", request.url, "\" must be http or https with a host"));
  }

  return absl::OkStatus();
}

absl::Status Validate(const PreheatRequest& request) {
  return ValidateReplicas(request.replicas);
}

absl::Status Validate(const PreheatImageRequest& request) {
  if (auto status = ValidateReplicas(request.replicas); !status.ok()) {
    return status;
  }

  if (request.concurrent_task_count <= 0) {
    return absl::InvalidArgumentError("concurrent task count must be positive");
  }

  return ValidatePlatform(request.platform);
}

absl::Status Validate(const StatImageRequest& request) {
  if (auto status = ValidateConcurrentPeerCount(request.concurrent_peer_count);
      !status.ok()) {
    return status;
  }

  return ValidatePlatform(request.platform);
}

absl::Status Validate(const DeleteRequest& request) {
  return ValidateReplicas(request.replicas);
}

absl::Status Validate(const DeleteImageRequest& request) {
  if (auto status = ValidateConcurrentPeerCount(request.concurrent_peer_count);
      !status.ok()) {
    return status;
  }

  return ValidatePlatform(request.platform);
}

void SetHeader(Header* header, absl::string_view name,
               absl::string_view value) {
  for (auto it = header->begin(); it != header->end();) {
    it = absl::EqualsIgnoreCase(it->first, name) ? header->erase(it)
                                                 : std::next(it);
  }

  header->emplace(name, value);
}

absl::StatusOr<Header> ProxyHeaders(const GetRequest& request) {
  Header headers = request.header;
  auto set = [&headers](absl::string_view name, absl::string_view value) {
    SetHeader(&headers, name, value);
  };

  if (request.piece_length) {
    set("X-Dragonfly-Piece-Length", absl::StrCat(*request.piece_length));
  }

  if (!request.tag.empty()) {
    set("X-Dragonfly-Tag", request.tag);
  }

  if (!request.application.empty()) {
    set("X-Dragonfly-Application", request.application);
  }

  if (!request.content_for_calculating_task_id.empty()) {
    set("X-Dragonfly-Content-For-Calculating-Task-ID",
        request.content_for_calculating_task_id);
  }

  set("X-Dragonfly-Enable-Task-ID-Based-Blob-Digest",
      request.enable_task_id_based_blob_digest ? "true" : "false");

  if (request.priority) {
    set("X-Dragonfly-Priority", absl::StrCat(*request.priority));
  }

  if (!request.filtered_query_params.empty()) {
    set("X-Dragonfly-Filtered-Query-Params",
        absl::StrJoin(request.filtered_query_params, ","));
  }

  set("X-Dragonfly-Use-P2P", "true");

  // Reject the headers HTTP cannot send: a name that is not a token, a value
  // with a control character other than a tab.
  for (const auto& [name, value] : headers) {
    if (name.empty() || !std::all_of(name.begin(), name.end(), [](char c) {
          return absl::ascii_isalnum(c) ||
                 absl::StrContains("!#$%&'*+-.^_`|~", c);
        })) {
      return absl::InvalidArgumentError(
          absl::StrCat("invalid header name \"", absl::CEscape(name), "\""));
    }

    if (std::any_of(value.begin(), value.end(), [](char c) {
          return absl::ascii_iscntrl(c) && c != '\t';
        })) {
      return absl::InvalidArgumentError(
          absl::StrCat("invalid value of header \"", name, "\""));
    }
  }

  return headers;
}

absl::StatusOr<std::string> NormalizePlatform(absl::string_view platform) {
  // The platform is "os/arch" or "os/arch/variant", each of letters, digits,
  // '_' and '-'.
  const std::vector<std::string> parts =
      absl::StrSplit(absl::AsciiStrToLower(platform), '/');
  if ((parts.size() != 2 && parts.size() != 3) ||
      !std::all_of(parts.begin(), parts.end(), [](const std::string& part) {
        return !part.empty() &&
               std::all_of(part.begin(), part.end(), [](char c) {
                 return absl::ascii_isalnum(c) || c == '_' || c == '-';
               });
      })) {
    return absl::InvalidArgumentError(
        absl::StrCat("invalid platform format \"", platform,
                     R"(", expected "os/arch" (e.g., "linux/amd64"))"));
  }

  // Normalize the aliases of the os and the architecture as containerd does.
  absl::string_view os = parts[0];
  absl::string_view arch = parts[1];
  if (os == "macos") {
    os = "darwin";
  }

  if (arch == "i386") {
    arch = "386";
  } else if (arch == "x86_64" || arch == "x86-64") {
    arch = "amd64";
  } else if (arch == "aarch64") {
    arch = "arm64";
  } else if (arch == "armhf" || arch == "armel") {
    arch = "arm";
  }

  return absl::StrCat(os, "/", arch);
}

}  // namespace internal
}  // namespace dragonfly::client_request
