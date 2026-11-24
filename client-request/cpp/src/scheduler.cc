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

#include "src/scheduler.h"

#include <cstddef>
#include <utility>

#include "absl/memory/memory.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/string_view.h"
#include "grpc/health/v1/health.grpc.pb.h"
#include "src/grpc_util.h"
#include "src/net.h"
#include "src/parallel.h"

namespace dragonfly::client_request {
namespace internal {
namespace {

// The host type of seed peers, refer to
// dragonfly-client-config/src/dfdaemon.rs#HostType.
constexpr std::uint32_t kSeedPeerType = 1;

// The health check timeout for seed peers.
constexpr std::chrono::seconds kHealthCheckTimeout(3);

// Reports whether the seed peer at the address is serving.
bool IsHealthy(const std::string& address) {
  grpc::ClientContext context;
  SetTimeout(&context, kHealthCheckTimeout);
  grpc::health::v1::HealthCheckResponse response;
  return grpc::health::v1::Health::NewStub(NewChannel(address))
             ->Check(&context, grpc::health::v1::HealthCheckRequest(),
                     &response)
             .ok() &&
         response.status() == grpc::health::v1::HealthCheckResponse::SERVING;
}

}  // namespace

absl::StatusOr<std::string> SchedulerTarget(const std::string& endpoint) {
  // The target is the authority of the endpoint.
  absl::string_view target = endpoint;
  const auto scheme = target.find("://");
  if (scheme != absl::string_view::npos) {
    target.remove_prefix(scheme + 3);
    target = target.substr(0, target.find_first_of("/?#"));
  }

  if (scheme == absl::string_view::npos || target.empty()) {
    return absl::InvalidArgumentError(
        absl::StrCat("invalid scheduler endpoint: ", endpoint));
  }

  return std::string(target);
}

Scheduler::Scheduler(const std::string& target,
                     std::chrono::milliseconds request_timeout)
    : stub_(scheduler::v2::Scheduler::NewStub(NewChannel(target))),
      request_timeout_(request_timeout) {}

absl::StatusOr<std::vector<SeedPeer>> Scheduler::ListSeedPeers() {
  grpc::ClientContext context;
  SetTimeout(&context, request_timeout_);
  scheduler::v2::ListHostsRequest request;
  request.set_type(kSeedPeerType);
  scheduler::v2::ListHostsResponse response;
  if (grpc::Status status = stub_->ListHosts(&context, request, &response);
      !status.ok()) {
    return FromGrpcStatus(status, "failed to list hosts");
  }

  // The number of seed peers in a cluster is usually small, so check all of
  // them simultaneously.
  std::vector<SeedPeer> seed_peers(response.hosts_size());
  (void)ParallelFor(seed_peers.size(), seed_peers.size(), [&](std::size_t i) {
    const common::v2::Host& host = response.hosts(static_cast<int>(i));
    SeedPeer& seed_peer = seed_peers[i];
    seed_peer.name = host.name();
    seed_peer.hostname = host.hostname();
    seed_peer.ip = host.ip();
    seed_peer.port = host.port();
    seed_peer.proxy_port = host.proxy_port();
    seed_peer.healthy = IsHealthy(JoinHostPort(host.ip(), host.port()));
    return absl::OkStatus();
  });

  return seed_peers;
}

}  // namespace internal

absl::StatusOr<std::unique_ptr<SchedulerClient>> SchedulerClient::Create(
    const std::string& endpoint, std::chrono::milliseconds request_timeout) {
  auto target = internal::SchedulerTarget(endpoint);
  if (!target.ok()) {
    return target.status();
  }

  return absl::WrapUnique(new SchedulerClient(
      std::make_unique<internal::Scheduler>(*target, request_timeout)));
}

SchedulerClient::SchedulerClient(std::unique_ptr<internal::Scheduler> scheduler)
    : scheduler_(std::move(scheduler)) {}

SchedulerClient::~SchedulerClient() = default;

absl::StatusOr<std::vector<SeedPeer>> SchedulerClient::ListSeedPeers() {
  auto seed_peers = scheduler_->ListSeedPeers();
  if (!seed_peers.ok()) {
    return absl::Status(seed_peers.status().code(),
                        absl::StrCat("failed to list seed peers: ",
                                     seed_peers.status().message()));
  }

  return seed_peers;
}

}  // namespace dragonfly::client_request
