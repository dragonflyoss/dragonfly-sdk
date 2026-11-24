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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_GET_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_GET_H_

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <functional>
#include <memory>
#include <optional>
#include <ostream>
#include <string>
#include <vector>

#include "absl/status/statusor.h"
#include "dragonfly/client_request/request.h"
#include "src/http_client.h"
#include "src/retry.h"

namespace dragonfly::client_request::internal {

/// The attempts of one request across the endpoints serving it: the shuffled
/// endpoints, the next one to aim at and the retries left. It keeps them
/// across runs, so a body that breaks off is sent again within the same
/// retries.
class Attempts {
 public:
  /// Sends a request once to the seed peer proxy at an endpoint.
  using Send = std::function<absl::StatusOr<std::unique_ptr<HttpResponse>>(
      const std::string& endpoint)>;

  /// Returns the attempts of a request across the endpoints, shuffled once,
  /// failing when there is none.
  static absl::StatusOr<Attempts> Create(const RetryPolicy& retry,
                                         std::vector<std::string> endpoints);

  /// Runs send until it succeeds, fails definitively or no retry is left,
  /// every attempt aimed at the next endpoint, wrapping around.
  absl::StatusOr<std::unique_ptr<HttpResponse>> Run(const Send& send);

  /// Takes a retry and returns the delay before it, nullopt once none is
  /// left.
  std::optional<std::chrono::milliseconds> NextDelay();

 private:
  Attempts(const RetryPolicy& retry, std::vector<std::string> endpoints);

  std::vector<std::string> endpoints_;
  std::size_t next_ = 0;
  std::uint8_t retries_;
  Delays delays_;
};

/// Sends a request once to the seed peer proxy at an endpoint.
class Sender {
 public:
  virtual ~Sender() = default;

  /// Sends the request once to the seed peer proxy at the endpoint.
  virtual absl::StatusOr<std::unique_ptr<HttpResponse>> SendTo(
      const std::string& endpoint, const HttpRequest& request) = 0;
};

/// Returns the request sent to the seed peer proxies for a GET request: its
/// canonical url with the P2P headers applied.
absl::StatusOr<HttpRequest> NewProxyRequest(
    const GetRequest& request, std::chrono::milliseconds read_timeout);

/// Sends the request once through the client to the seed peer proxy at the
/// endpoint. A non-2xx answer becomes an error by its X-Dragonfly-Error-Type
/// header.
absl::StatusOr<std::unique_ptr<HttpResponse>> SendGet(
    HttpClient& client, const std::string& endpoint,
    const HttpRequest& request);

/// Sends GET requests across the seed peer proxies serving them through a
/// sender, a failed attempt retried on the next one. It is thread-safe.
class ProxyGetter {
 public:
  ProxyGetter(std::shared_ptr<Sender> sender, RetryPolicy retry,
              std::chrono::milliseconds read_timeout);

  /// Sends the request across the endpoints and returns the response with a
  /// streaming body, which is sent again on the next endpoint for the bytes
  /// left when it breaks off.
  absl::StatusOr<GetResponse> Get(const GetRequest& request,
                                  std::vector<std::string> endpoints) const;

  /// Sends the request like Get and writes the response body into the output.
  absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                      std::vector<std::string> endpoints,
                                      std::ostream& output) const;

 private:
  const std::shared_ptr<Sender> sender_;
  const RetryPolicy retry_;
  const std::chrono::milliseconds read_timeout_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_GET_H_
