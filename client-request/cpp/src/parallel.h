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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_PARALLEL_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_PARALLEL_H_

#include <cstddef>
#include <functional>

#include "absl/status/status.h"

namespace dragonfly::client_request::internal {

/// Runs fn(i) for every i below count on up to limit threads, the calling one
/// included, and returns the first failure, after which no further i is
/// started. Every concurrent call takes a thread of its own.
absl::Status ParallelFor(std::size_t count, std::size_t limit,
                         const std::function<absl::Status(std::size_t)>& fn);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_PARALLEL_H_
