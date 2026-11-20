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

#ifndef DRAGONFLY_CLIENT_REQUEST_PROXY_WITH_SEED_PEERS_H_
#define DRAGONFLY_CLIENT_REQUEST_PROXY_WITH_SEED_PEERS_H_

#include <chrono>
#include <cstdint>
#include <memory>
#include <optional>
#include <ostream>
#include <string>
#include <vector>

#include "absl/status/statusor.h"
#include "dragonfly/client_request/proxy.h"
#include "dragonfly/client_request/request.h"

namespace dragonfly::client_request {

/// The options of a ProxyWithSeedPeers.
struct ProxyWithSeedPeersOptions {
  /// How many times a request is retried after a transient failure, each
  /// retry on the next seed peer, at most 10.
  std::uint8_t max_retries = 1;

  /// The delay between retries, retrying at once when unset.
  std::optional<ExponentialBackoff> backoff;

  /// The longest wait for a response and for every read of its body.
  std::chrono::milliseconds read_timeout = std::chrono::seconds(60);
};

/// The client that sends requests via the seed peers given at construction,
/// selected by the consistent hash ring like Proxy without connecting to the
/// scheduler. It is thread-safe.
class ProxyWithSeedPeers final : public RequestWithSeedPeers {
 public:
  /// Creates a ProxyWithSeedPeers that sends requests via the seed peers,
  /// such as the ones SchedulerClient::ListSeedPeers returns.
  static absl::StatusOr<std::unique_ptr<ProxyWithSeedPeers>> Create(
      const std::vector<SeedPeer>& seed_peers,
      const ProxyWithSeedPeersOptions& options = {});

  ~ProxyWithSeedPeers() override;
  ProxyWithSeedPeers(const ProxyWithSeedPeers&) = delete;
  ProxyWithSeedPeers& operator=(const ProxyWithSeedPeers&) = delete;

  /// Replaces the seed peers, such as with the ones listed again. The hash
  /// ring is rebuilt only when the seed peers change, and invalid seed peers,
  /// such as none, are ignored.
  void UpdateSeedPeers(const std::vector<SeedPeer>& seed_peers);

  /// Sends a GET request via the seed peers, see Proxy::Get.
  absl::StatusOr<GetResponse> Get(const GetRequest& request) override;

  /// Sends a GET request via the seed peers, see Proxy::GetInto.
  absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                      std::ostream& output) override;

  /// Returns the proxy endpoints of the seed peers that would serve the
  /// request, see Proxy::LookupEndpoints.
  absl::StatusOr<std::vector<std::string>> LookupEndpoints(
      const GetRequest& request) override;

 private:
  explicit ProxyWithSeedPeers(std::unique_ptr<internal::ProxyImpl> impl);

  std::unique_ptr<internal::ProxyImpl> impl_;
};

}  // namespace dragonfly::client_request

#endif  // DRAGONFLY_CLIENT_REQUEST_PROXY_WITH_SEED_PEERS_H_
