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

#include "src/retry.h"

#include <gtest/gtest.h>

#include <chrono>
#include <cstdint>
#include <vector>

namespace dragonfly::client_request::internal {
namespace {

using std::chrono::milliseconds;

TEST(DelaysTest, IsZeroWithoutABackoff) {
  Delays delays(std::nullopt);
  EXPECT_EQ(delays.Next(), milliseconds(0));
  EXPECT_EQ(delays.Next(), milliseconds(0));
}

TEST(DelaysTest, GrowsByTheMultiplierUpToTheMaxInterval) {
  ExponentialBackoff backoff;
  backoff.initial_interval = milliseconds(100);
  backoff.multiplier = 2;
  backoff.randomization_factor = 0;
  backoff.max_interval = milliseconds(350);

  Delays delays(backoff);
  EXPECT_EQ(delays.Next(), milliseconds(100));
  EXPECT_EQ(delays.Next(), milliseconds(200));
  EXPECT_EQ(delays.Next(), milliseconds(350));
  EXPECT_EQ(delays.Next(), milliseconds(350));
}

TEST(DelaysTest, StaysWithinTheRandomizationFactor) {
  ExponentialBackoff backoff;
  backoff.initial_interval = milliseconds(100);
  backoff.multiplier = 1;
  backoff.randomization_factor = 0.5;

  Delays delays(backoff);
  for (int i = 0; i < 100; ++i) {
    milliseconds delay = delays.Next();
    EXPECT_GE(delay, milliseconds(50));
    EXPECT_LE(delay, milliseconds(150));
  }
}

TEST(DelaysTest, ToleratesANegativeRandomizationFactor) {
  ExponentialBackoff backoff;
  backoff.initial_interval = milliseconds(100);
  backoff.multiplier = 1.0;
  backoff.randomization_factor = -0.5;
  Delays delays(backoff);
  for (int i = 0; i < 100; ++i) {
    const milliseconds delay = delays.Next();
    EXPECT_GE(delay, milliseconds(50));
    EXPECT_LE(delay, milliseconds(150));
  }
}

TEST(RetryPolicyTest, RunRetriesOnlyTransientFailures) {
  struct TestCase {
    std::uint8_t max_retries;
    std::vector<absl::Status> answers;
    absl::StatusCode expected_code;
    int expected_attempts;
  };

  const TestCase test_cases[] = {
      {1, {absl::OkStatus()}, absl::StatusCode::kOk, 1},
      {1,
       {absl::InternalError("boom"), absl::OkStatus()},
       absl::StatusCode::kOk,
       2},
      {1,
       {absl::InternalError("boom"), absl::UnavailableError("down")},
       absl::StatusCode::kUnavailable,
       2},
      {3,
       {absl::InternalError("boom"), absl::NotFoundError("gone")},
       absl::StatusCode::kNotFound,
       2},
      {3,
       {absl::InvalidArgumentError("bad")},
       absl::StatusCode::kInvalidArgument,
       1},
      {0, {absl::InternalError("boom")}, absl::StatusCode::kInternal, 1},
  };

  for (const auto& test_case : test_cases) {
    const RetryPolicy policy(test_case.max_retries);

    int attempts = 0;
    absl::Status status =
        policy.Run([&] { return test_case.answers[attempts++]; });
    EXPECT_EQ(status.code(), test_case.expected_code);
    EXPECT_EQ(attempts, test_case.expected_attempts);
  }
}

}  // namespace
}  // namespace dragonfly::client_request::internal
