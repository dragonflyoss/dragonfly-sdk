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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_ERRORS_INTERNAL_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_ERRORS_INTERNAL_H_

#include "absl/status/status.h"
#include "dragonfly/client_request/errors.h"

namespace dragonfly::client_request::internal {

/// Returns the error of a non-2xx answer, carrying the detail.
absl::Status AnswerError(const ErrorDetail& detail);

/// Reports whether a failed attempt is worth another one.
///
/// | Failure                                                         | Retry |
/// |-----------------------------------------------------------------|-------|
/// | kDeadlineExceeded, kInternal (timeout, connect, transport)      | yes   |
/// | proxy, backend or dfdaemon answer 5xx, 408 or 429               | yes   |
/// | gRPC kUnavailable, kUnknown, kAborted, kResourceExhausted       | yes   |
/// | any other answer or code, such as 404 or kInvalidArgument       | no    |
bool IsRetryable(const absl::Status& status);

/// Reports whether the HTTP status of an answer marks a transient failure:
/// 5xx, 408 or 429.
bool IsRetryableStatusCode(int status_code);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_ERRORS_INTERNAL_H_
