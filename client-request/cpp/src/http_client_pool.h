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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_HTTP_CLIENT_POOL_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_HTTP_CLIENT_POOL_H_

#include <chrono>
#include <cstddef>
#include <memory>
#include <mutex>
#include <string>
#include <unordered_map>

#include "src/http_client.h"

namespace dragonfly::client_request::internal {

/// A pool of the clients of the seed peer proxies by endpoint, dropping the
/// idle ones. It is thread-safe.
class HttpClientPool {
 public:
  /// Creates a pool that keeps up to capacity clients, each until it has
  /// been idle for the timeout.
  HttpClientPool(std::size_t capacity, std::chrono::milliseconds idle_timeout);

  /// Gets or creates the client of the endpoint, used at the time given.
  std::shared_ptr<HttpClient> Entry(const std::string& endpoint,
                                    std::chrono::steady_clock::time_point now =
                                        std::chrono::steady_clock::now());

  /// Returns the number of clients in the pool.
  std::size_t size() const;

 private:
  /// A client with the last time it was used.
  struct Slot {
    std::shared_ptr<HttpClient> client;
    std::chrono::steady_clock::time_point last_used_at;
  };

  const std::size_t capacity_;
  const std::chrono::milliseconds idle_timeout_;

  /// Protects clients_ and cleanup_at_.
  mutable std::mutex mu_;
  std::unordered_map<std::string, Slot> clients_;
  std::chrono::steady_clock::time_point cleanup_at_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_HTTP_CLIENT_POOL_H_
