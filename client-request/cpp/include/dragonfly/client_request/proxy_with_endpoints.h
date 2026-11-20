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

#ifndef DRAGONFLY_CLIENT_REQUEST_PROXY_WITH_ENDPOINTS_H_
#define DRAGONFLY_CLIENT_REQUEST_PROXY_WITH_ENDPOINTS_H_

#include <chrono>
#include <cstdint>
#include <memory>
#include <optional>
#include <ostream>
#include <string>
#include <vector>

#include "absl/status/statusor.h"
#include "dragonfly/client_request/request.h"

namespace dragonfly::client_request {

namespace internal {
class ProxyWithEndpointsImpl;
}  // namespace internal

/// The options of a ProxyWithEndpoints.
struct ProxyWithEndpointsOptions {
  /// How many times a request is retried after a transient failure, each
  /// retry on the next endpoint, at most 3.
  std::uint8_t max_retries = 1;

  /// The delay between retries, retrying at once when unset.
  std::optional<ExponentialBackoff> backoff;

  /// The longest wait for a response and for every read of its body.
  std::chrono::milliseconds read_timeout = std::chrono::seconds(60);
};

/// The client that sends requests via fixed seed peer endpoints, typically
/// from Proxy::LookupEndpoints, so no scheduler is consulted. It is
/// thread-safe.
class ProxyWithEndpoints final : public RequestWithEndpoints {
 public:
  /// Creates a ProxyWithEndpoints that sends requests via the seed peer
  /// endpoints, such as "http://127.0.0.1:4001".
  static absl::StatusOr<std::unique_ptr<ProxyWithEndpoints>> Create(
      std::vector<std::string> endpoints,
      const ProxyWithEndpointsOptions& options = {});

  ~ProxyWithEndpoints() override;
  ProxyWithEndpoints(const ProxyWithEndpoints&) = delete;
  ProxyWithEndpoints& operator=(const ProxyWithEndpoints&) = delete;

  /// Sends a GET request to a randomly picked endpoint and returns a response
  /// with a streaming body. A transient failure is retried on the others.
  absl::StatusOr<GetResponse> Get(const GetRequest& request) override;

  /// Sends a GET request to a randomly picked endpoint and writes the
  /// response body into the output, which is never rewound.
  absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                      std::ostream& output) override;

 private:
  explicit ProxyWithEndpoints(
      std::unique_ptr<internal::ProxyWithEndpointsImpl> impl);

  std::unique_ptr<internal::ProxyWithEndpointsImpl> impl_;
};

}  // namespace dragonfly::client_request

#endif  // DRAGONFLY_CLIENT_REQUEST_PROXY_WITH_ENDPOINTS_H_
