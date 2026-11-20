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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_RETRY_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_RETRY_H_

#include <chrono>
#include <cstdint>
#include <functional>
#include <optional>

#include "absl/status/status.h"
#include "dragonfly/client_request/request.h"

namespace dragonfly::client_request::internal {

/// The delays before the retries of one request: the exponential sequence of
/// the backoff, zero delays without one.
class Delays {
 public:
  explicit Delays(const std::optional<ExponentialBackoff>& backoff);

  /// Returns the delay before the next retry.
  std::chrono::milliseconds Next();

 private:
  std::optional<ExponentialBackoff> backoff_;
  double current_interval_ = 0;
};

/// How often a request is retried and how long each retry waits.
class RetryPolicy {
 public:
  /// Creates a policy of max_retries retries after the first attempt, each
  /// waiting for the delay of the backoff, retrying at once without one.
  explicit RetryPolicy(std::uint8_t max_retries = 1,
                       std::optional<ExponentialBackoff> backoff = std::nullopt)
      : max_retries_(max_retries), backoff_(backoff) {}

  /// Returns the retries after the first attempt.
  std::uint8_t max_retries() const { return max_retries_; }

  /// Returns the backoff of the delay before each retry.
  const std::optional<ExponentialBackoff>& backoff() const { return backoff_; }

  /// Runs the attempt until it succeeds, fails definitively, or has been
  /// retried max_retries times.
  absl::Status Run(const std::function<absl::Status()>& attempt) const;

 private:
  std::uint8_t max_retries_;
  std::optional<ExponentialBackoff> backoff_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_RETRY_H_
