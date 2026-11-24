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

#ifndef DRAGONFLY_CLIENT_REQUEST_PROXY_H_
#define DRAGONFLY_CLIENT_REQUEST_PROXY_H_

#include <chrono>
#include <cstdint>
#include <memory>
#include <optional>
#include <ostream>
#include <string>
#include <vector>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "dragonfly/client_request/request.h"

namespace dragonfly::client_request {

namespace internal {
class ProxyImpl;
class Scheduler;
}  // namespace internal

/// A seed peer listed by the scheduler, with the result of its health check.
struct SeedPeer {
  /// The name of the host, the key of the seed peer on the hash ring.
  std::string name;

  /// The hostname of the seed peer.
  std::string hostname;

  /// The ip of the seed peer.
  std::string ip;

  /// The port of the gRPC server of the seed peer.
  std::int32_t port = 0;

  /// The port of the proxy of the seed peer.
  std::int32_t proxy_port = 0;

  /// Whether the seed peer passed its health check.
  bool healthy = false;
};

/// The client of the scheduler for listing the seed peers to share through
/// ProxyWithSeedPeers.
class SchedulerClient {
 public:
  /// Creates a client of the scheduler at the endpoint, such as
  /// "http://scheduler-service:8002", bounding its requests by the timeout.
  static absl::StatusOr<std::unique_ptr<SchedulerClient>> Create(
      const std::string& endpoint, std::chrono::milliseconds request_timeout);

  ~SchedulerClient();
  SchedulerClient(const SchedulerClient&) = delete;
  SchedulerClient& operator=(const SchedulerClient&) = delete;

  /// Lists the seed peers with their health, taking up to the request timeout
  /// plus 3 seconds of health checks.
  absl::StatusOr<std::vector<SeedPeer>> ListSeedPeers();

 private:
  explicit SchedulerClient(std::unique_ptr<internal::Scheduler> scheduler);

  std::unique_ptr<internal::Scheduler> scheduler_;
};

/// The options of a Proxy.
struct ProxyOptions {
  /// How many times a request is retried after a transient failure, each
  /// retry on the next seed peer, at most 10.
  std::uint8_t max_retries = 1;

  /// The delay between retries, retrying at once when unset.
  std::optional<ExponentialBackoff> backoff;

  /// The longest wait for a response and for every read of its body.
  std::chrono::milliseconds read_timeout = std::chrono::seconds(60);

  /// The timeout of requests to the scheduler, at least 100 milliseconds.
  std::chrono::milliseconds scheduler_request_timeout = std::chrono::seconds(5);

  /// The interval of the health check of the seed peers, 1 to 600 seconds.
  std::chrono::milliseconds health_check_interval = std::chrono::seconds(60);
};

/// The client that sends requests via the Dragonfly. It is thread-safe.
///
/// A request is served by the seed peers the consistent hash ring picks for
/// its task, and a failed attempt is retried on the next seed peer:
///
///     client ──lookup──▶ hash ring ──▶ seed peers [B, A]
///
///     attempt 1 ──GET──▶ B ──connect error, 5xx, 408, 429──▶ attempt 2 ──▶ A
///                        └─ any other answer, such as 404 ──▶ fails at once
class Proxy final : public Request {
 public:
  /// Creates a Proxy that lists the seed peers from the scheduler at the
  /// endpoint, such as "http://scheduler-service:8002", and refreshes them in
  /// the background.
  static absl::StatusOr<std::unique_ptr<Proxy>> Create(
      const std::string& scheduler_endpoint, const ProxyOptions& options = {});

  /// Stops the background refresh and closes the scheduler connection.
  ~Proxy() override;
  Proxy(const Proxy&) = delete;
  Proxy& operator=(const Proxy&) = delete;

  /// Sends a GET request via the Dragonfly and returns a response with a
  /// streaming body.
  absl::StatusOr<GetResponse> Get(const GetRequest& request) override;

  /// Sends a GET request via the Dragonfly and writes the response body into
  /// the output, which is never rewound.
  absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                      std::ostream& output) override;

  /// Preheats a file: has every replica seed peer, or every seed peer with
  /// Scope::kAllSeedPeers, download it. A transient failure is retried on the
  /// same seed peer.
  absl::Status Preheat(const PreheatRequest& request) override;

  /// Preheats an OCI image: resolves its manifests, all the platforms unless
  /// a platform is set, and has the seed peers download every manifest and
  /// blob.
  absl::Status PreheatImage(const PreheatImageRequest& request) override;

  /// Reports which healthy seed peers hold which layers of an OCI image.
  absl::StatusOr<StatImageResponse> StatImage(
      const StatImageRequest& request) override;

  /// Deletes a preheated file from every replica seed peer, or every seed
  /// peer with Scope::kAllSeedPeers. A seed peer without the task counts as
  /// deleted.
  absl::Status Delete(const DeleteRequest& request) override;

  /// Deletes a preheated OCI image from every healthy seed peer, retaining
  /// the tasks other images or platforms reference.
  absl::StatusOr<DeleteImageResponse> DeleteImage(
      const DeleteImageRequest& request) override;

  /// Returns the proxy endpoints, such as "http://127.0.0.1:4001", of the
  /// seed peers that would serve the request, in the consistent hash ring
  /// order, up to the replicas and skipping the unhealthy or ejected ones.
  absl::StatusOr<std::vector<std::string>> LookupEndpoints(
      const GetRequest& request) override;

 private:
  explicit Proxy(std::unique_ptr<internal::ProxyImpl> impl);

  std::unique_ptr<internal::ProxyImpl> impl_;
};

}  // namespace dragonfly::client_request

#endif  // DRAGONFLY_CLIENT_REQUEST_PROXY_H_
