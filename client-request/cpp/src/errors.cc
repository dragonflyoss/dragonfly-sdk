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

#include "dragonfly/client_request/errors.h"

#include <string>
#include <utility>
#include <vector>

#include "absl/strings/cord.h"
#include "absl/strings/numbers.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_split.h"
#include "src/errors_internal.h"

namespace dragonfly::client_request {
namespace {

// The payload of an answer error: the type, the status code and the headers
// on a line each, an empty line, then the message.
constexpr char kErrorDetailUrl[] =
    "type.dragonfly.io/client_request.ErrorDetail";

absl::StatusCode CodeFromHttpStatus(int status_code) {
  switch (status_code) {
    case 400:
      return absl::StatusCode::kInvalidArgument;
    case 401:
      return absl::StatusCode::kUnauthenticated;
    case 403:
      return absl::StatusCode::kPermissionDenied;
    case 404:
      return absl::StatusCode::kNotFound;
    case 408:
    case 504:
      return absl::StatusCode::kDeadlineExceeded;
    case 416:
      return absl::StatusCode::kOutOfRange;
    case 429:
      return absl::StatusCode::kResourceExhausted;
    case 501:
      return absl::StatusCode::kUnimplemented;
    case 503:
      return absl::StatusCode::kUnavailable;
    default:
      return status_code >= 500 ? absl::StatusCode::kInternal
                                : absl::StatusCode::kUnknown;
  }
}

const char* ErrorTypeName(ErrorType type) {
  switch (type) {
    case ErrorType::kBackend:
      return "backend";
    case ErrorType::kDfdaemon:
      return "dfdaemon";
    case ErrorType::kProxy:
      return "proxy";
  }

  return "proxy";
}

}  // namespace

std::optional<ErrorDetail> GetErrorDetail(const absl::Status& status) {
  auto payload = status.GetPayload(kErrorDetailUrl);
  if (!payload) {
    return std::nullopt;
  }

  std::string encoded(*payload);
  auto separator = encoded.find("\n\n");
  if (separator == std::string::npos) {
    return std::nullopt;
  }

  std::vector<std::string> lines =
      absl::StrSplit(encoded.substr(0, separator), '\n');
  ErrorDetail detail;
  if (lines.size() < 2 || !absl::SimpleAtoi(lines[1], &detail.status_code)) {
    return std::nullopt;
  }

  if (lines[0] == "backend") {
    detail.type = ErrorType::kBackend;
  } else if (lines[0] == "dfdaemon") {
    detail.type = ErrorType::kDfdaemon;
  }

  for (std::size_t i = 2; i < lines.size(); ++i) {
    std::pair<std::string, std::string> header =
        absl::StrSplit(lines[i], absl::MaxSplits(": ", 1));
    detail.header.insert(std::move(header));
  }

  detail.message = encoded.substr(separator + 2);
  return detail;
}

namespace internal {

absl::Status AnswerError(const ErrorDetail& detail) {
  const char* type = ErrorTypeName(detail.type);
  std::string encoded = absl::StrCat(type, "\n", detail.status_code);
  for (const auto& [name, value] : detail.header) {
    absl::StrAppend(&encoded, "\n", name, ": ", value);
  }
  absl::StrAppend(&encoded, "\n\n", detail.message);

  absl::Status status(
      CodeFromHttpStatus(detail.status_code),
      absl::StrCat(type, " error, status code: ", detail.status_code,
                   ", message: ", detail.message));
  status.SetPayload(kErrorDetailUrl, absl::Cord(encoded));
  return status;
}

bool IsRetryable(const absl::Status& status) {
  if (auto detail = GetErrorDetail(status)) {
    return IsRetryableStatusCode(detail->status_code);
  }

  switch (status.code()) {
    case absl::StatusCode::kDeadlineExceeded:
    case absl::StatusCode::kInternal:
    case absl::StatusCode::kUnavailable:
    case absl::StatusCode::kUnknown:
    case absl::StatusCode::kAborted:
    case absl::StatusCode::kResourceExhausted:
      return true;
    default:
      return false;
  }
}

bool IsRetryableStatusCode(int status_code) {
  return status_code >= 500 || status_code == 408 || status_code == 429;
}

}  // namespace internal
}  // namespace dragonfly::client_request
