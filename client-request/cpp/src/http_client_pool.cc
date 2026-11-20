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

#include "src/http_client_pool.h"

#include <iterator>

namespace dragonfly::client_request::internal {

HttpClientPool::HttpClientPool(std::size_t capacity,
                               std::chrono::milliseconds idle_timeout)
    : capacity_(capacity),
      idle_timeout_(idle_timeout),
      cleanup_at_(std::chrono::steady_clock::now()) {}

std::shared_ptr<HttpClient> HttpClientPool::Entry(
    const std::string& endpoint, std::chrono::steady_clock::time_point now) {
  std::lock_guard<std::mutex> lock(mu_);

  // Drop the idle clients that exceed the capacity or the idle timeout,
  // keeping those with active requests. A cleanup too soon after the last
  // one is skipped.
  if (now - cleanup_at_ >= idle_timeout_ / 2) {
    const bool exceeds_capacity = clients_.size() > capacity_;
    for (auto it = clients_.begin(); it != clients_.end();) {
      const bool is_recent = now - it->second.last_used_at <= idle_timeout_;
      const bool has_active_requests = it->second.client.use_count() > 1;
      it = has_active_requests || (!exceeds_capacity && is_recent)
               ? std::next(it)
               : clients_.erase(it);
    }

    cleanup_at_ = now;
  }

  Slot& slot = clients_[endpoint];
  if (slot.client == nullptr) {
    slot.client = HttpClient::Create();
  }

  slot.last_used_at = now;
  return slot.client;
}

std::size_t HttpClientPool::size() const {
  std::lock_guard<std::mutex> lock(mu_);
  return clients_.size();
}

}  // namespace dragonfly::client_request::internal
