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

#include "src/parallel.h"

#include <algorithm>
#include <atomic>
#include <mutex>
#include <system_error>
#include <thread>
#include <utility>
#include <vector>

namespace dragonfly::client_request::internal {

absl::Status ParallelFor(std::size_t count, std::size_t limit,
                         const std::function<absl::Status(std::size_t)>& fn) {
  std::atomic<std::size_t> next{0};
  std::atomic<bool> failed{false};
  std::mutex mu;
  absl::Status first_failure;

  auto worker = [&] {
    for (std::size_t i = next++; i < count && !failed; i = next++) {
      absl::Status status = fn(i);
      if (!status.ok() && !failed.exchange(true)) {
        std::scoped_lock lock(mu);
        first_failure = std::move(status);
      }
    }
  };

  // The workers share the indexes, so the ones started finish them all when
  // the system refuses another thread.
  std::vector<std::thread> threads;
  for (std::size_t i = 1; i < std::min(count, std::max<std::size_t>(limit, 1));
       ++i) {
    try {
      threads.emplace_back(worker);
    } catch (const std::system_error&) {
      break;
    }
  }

  worker();
  for (auto& thread : threads) {
    thread.join();
  }

  std::scoped_lock lock(mu);
  return first_failure;
}

}  // namespace dragonfly::client_request::internal
