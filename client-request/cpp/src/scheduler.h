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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_SCHEDULER_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_SCHEDULER_H_

#include <chrono>
#include <memory>
#include <string>
#include <vector>

#include "absl/status/statusor.h"
#include "dragonfly/client_request/proxy.h"
#include "scheduler.grpc.pb.h"

namespace dragonfly::client_request::internal {

/// Returns the gRPC target, the host and port, of a scheduler endpoint such
/// as "http://scheduler-service:8002".
absl::StatusOr<std::string> SchedulerTarget(const std::string& endpoint);

/// Lists the seed peers from the scheduler service. It is thread-safe.
class Scheduler {
 public:
  /// Creates a scheduler at the gRPC target, such as "scheduler-service:8002",
  /// its requests bounded by the timeout.
  Scheduler(const std::string& target,
            std::chrono::milliseconds request_timeout);

  /// Lists the seed peers with their health, taking up to the request timeout
  /// plus 3 seconds of health checks.
  absl::StatusOr<std::vector<SeedPeer>> ListSeedPeers();

 private:
  std::unique_ptr<scheduler::v2::Scheduler::Stub> stub_;
  std::chrono::milliseconds request_timeout_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_SCHEDULER_H_
