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

#ifndef DRAGONFLY_CLIENT_REQUEST_ERRORS_H_
#define DRAGONFLY_CLIENT_REQUEST_ERRORS_H_

#include <map>
#include <optional>
#include <string>

#include "absl/status/status.h"

namespace dragonfly::client_request {

/// The headers of a request or a response. The names of a response are
/// lowercased.
using Header = std::multimap<std::string, std::string>;

/// The server that answered a request with an error.
enum class ErrorType {
  /// The seed peer proxy.
  kProxy,
  /// The backend the seed peer downloads from.
  kBackend,
  /// The dfdaemon of the seed peer.
  kDfdaemon,
};

/// The answer of a server that failed a request.
struct ErrorDetail {
  /// The server that answered.
  ErrorType type = ErrorType::kProxy;

  /// The HTTP status code of the answer.
  int status_code = 0;

  /// The HTTP headers of the answer.
  Header header;

  /// The message of the answer.
  std::string message;
};

/// Returns the answer that failed the request, nullopt when no server
/// answered.
///
/// Every failure is an absl::Status:
///
/// | Failure                                  | Code                        |
/// |------------------------------------------|-----------------------------|
/// | invalid argument                         | kInvalidArgument            |
/// | request timeout                          | kDeadlineExceeded           |
/// | connect or transport error               | kInternal                   |
/// | answer of a proxy, backend or dfdaemon   | by HTTP status, with detail |
/// | gRPC status of a seed peer or scheduler  | the same code               |
std::optional<ErrorDetail> GetErrorDetail(const absl::Status& status);

}  // namespace dragonfly::client_request

#endif  // DRAGONFLY_CLIENT_REQUEST_ERRORS_H_
