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

#include <gtest/gtest.h>

#include <algorithm>
#include <atomic>
#include <chrono>
#include <condition_variable>
#include <mutex>
#include <set>

namespace dragonfly::client_request::internal {
namespace {

TEST(ParallelForTest, RunsEveryIndexOnTheLimitOfThreads) {
  std::mutex mu;
  std::condition_variable cv;
  int running = 0;
  int peak = 0;
  std::set<std::size_t> seen;

  absl::Status status = ParallelFor(16, 4, [&](std::size_t i) {
    std::unique_lock<std::mutex> lock(mu);
    peak = std::max(peak, ++running);
    seen.insert(i);
    cv.notify_all();
    cv.wait_for(lock, std::chrono::seconds(5), [&] { return peak == 4; });
    --running;
    return absl::OkStatus();
  });

  EXPECT_TRUE(status.ok());
  EXPECT_EQ(seen.size(), 16U);
  EXPECT_EQ(peak, 4);
}

TEST(ParallelForTest, ReturnsTheFirstFailureAndStopsStarting) {
  std::atomic<int> started{0};
  absl::Status status = ParallelFor(100, 1, [&](std::size_t i) {
    ++started;
    return i == 2 ? absl::NotFoundError("gone") : absl::OkStatus();
  });

  EXPECT_EQ(status.code(), absl::StatusCode::kNotFound);
  EXPECT_EQ(started, 3);
}

TEST(ParallelForTest, DoesNothingWithoutWork) {
  EXPECT_TRUE(ParallelFor(0, 4, [](std::size_t) {
                return absl::InternalError("unreachable");
              }).ok());
}

}  // namespace
}  // namespace dragonfly::client_request::internal
