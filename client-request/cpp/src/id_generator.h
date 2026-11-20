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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_ID_GENERATOR_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_ID_GENERATOR_H_

#include <cstdint>
#include <optional>
#include <string>
#include <vector>

#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"

namespace dragonfly::client_request::internal {

/// Returns the id of the task, identical to the ids the Go and Rust clients
/// generate. An empty tag, application or content is not set.
absl::StatusOr<std::string> TaskId(
    absl::string_view url, std::optional<std::uint64_t> piece_length,
    absl::string_view tag, absl::string_view application,
    const std::vector<std::string>& filtered_query_params,
    absl::string_view content_for_calculating_task_id,
    bool enable_task_id_based_blob_digest);

/// Returns the id of the task of a request, by its url and its task fields.
template <typename Request>
absl::StatusOr<std::string> TaskId(const Request& request) {
  return TaskId(request.url, request.piece_length, request.tag,
                request.application, request.filtered_query_params,
                request.content_for_calculating_task_id,
                request.enable_task_id_based_blob_digest);
}

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_ID_GENERATOR_H_
