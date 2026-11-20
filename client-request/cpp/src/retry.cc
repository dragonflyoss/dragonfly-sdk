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

#include <algorithm>
#include <cmath>
#include <random>
#include <thread>

#include "src/errors_internal.h"

namespace dragonfly::client_request::internal {

Delays::Delays(const std::optional<ExponentialBackoff>& backoff)
    : backoff_(backoff) {
  if (backoff_) {
    current_interval_ = static_cast<double>(backoff_->initial_interval.count());
  }
}

std::chrono::milliseconds Delays::Next() {
  if (!backoff_) {
    return std::chrono::milliseconds::zero();
  }

  // Pick the delay within the randomization factor around the interval.
  thread_local std::mt19937_64 generator(std::random_device{}());
  const double delta =
      std::abs(backoff_->randomization_factor * current_interval_);
  double delay = std::uniform_real_distribution<double>(
      current_interval_ - delta, current_interval_ + delta)(generator);

  current_interval_ =
      std::min(current_interval_ * backoff_->multiplier,
               static_cast<double>(backoff_->max_interval.count()));
  return std::chrono::milliseconds(
      static_cast<std::chrono::milliseconds::rep>(std::max(delay, 0.0)));
}

absl::Status RetryPolicy::Run(
    const std::function<absl::Status()>& attempt) const {
  Delays delays(backoff_);
  for (std::uint8_t retried = 0;; ++retried) {
    absl::Status status = attempt();
    if (status.ok() || !IsRetryable(status) || retried == max_retries_) {
      return status;
    }

    std::this_thread::sleep_for(delays.Next());
  }
}

}  // namespace dragonfly::client_request::internal
