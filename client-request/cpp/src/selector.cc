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

#include "src/selector.h"

#include <algorithm>
#include <cstddef>
#include <set>
#include <utility>

#include "absl/strings/str_cat.h"
#include "src/net.h"

namespace dragonfly::client_request::internal {

SeedPeerSelector::SeedPeerSelector(std::chrono::milliseconds ejection_cooldown)
    : ejection_cooldown_(ejection_cooldown) {}

SeedPeerSelector::~SeedPeerSelector() { Close(); }

absl::Status SeedPeerSelector::Refresh(Scheduler& scheduler) {
  // Only one refresh can be running at a time.
  std::unique_lock<std::mutex> lock(refresh_mu_, std::try_to_lock);
  if (!lock.owns_lock()) {
    return absl::OkStatus();
  }

  auto seed_peers = scheduler.ListSeedPeers();
  if (!seed_peers.ok()) {
    return seed_peers.status();
  }

  Update(*seed_peers);
  return absl::OkStatus();
}

void SeedPeerSelector::Run(std::shared_ptr<Scheduler> scheduler,
                           std::chrono::milliseconds health_check_interval) {
  thread_ = std::thread(
      [this, scheduler = std::move(scheduler), health_check_interval] {
        std::unique_lock<std::mutex> lock(close_mu_);
        while (!close_cv_.wait_for(lock, health_check_interval,
                                   [this] { return closed_; })) {
          lock.unlock();
          (void)Refresh(*scheduler);
          lock.lock();
        }
      });
}

void SeedPeerSelector::Close() {
  {
    std::scoped_lock lock(close_mu_);
    closed_ = true;
  }

  close_cv_.notify_all();
  if (thread_.joinable()) {
    thread_.join();
  }
}

void SeedPeerSelector::Update(const std::vector<SeedPeer>& seed_peers) {
  std::map<std::string, SeedPeer> hosts;
  for (const auto& seed_peer : seed_peers) {
    hosts[seed_peer.name] = seed_peer;
  }

  // Rebuilding the ring costs kDefaultVNodesPerNode hashes per host and a
  // sort, so reuse the current ring when the host names are unchanged and
  // only refresh the host records and their health.
  std::unique_lock<std::shared_mutex> lock(mu_);
  const bool unchanged = hosts.size() == hosts_.size() &&
                         std::equal(hosts.begin(), hosts.end(), hosts_.begin(),
                                    [](const auto& a, const auto& b) {
                                      return a.first == b.first;
                                    });
  if (!unchanged) {
    VNodeHashRing ring;
    for (const auto& host : hosts) {
      ring.Add(host.first);
    }

    ring_ = std::move(ring);
  }

  hosts_ = std::move(hosts);
}

void SeedPeerSelector::OnFailure(const std::string& endpoint) {
  std::unique_lock<std::shared_mutex> lock(ejections_mu_);
  Ejection& ejection = ejections_[endpoint];
  if (++ejection.failed_count >= kMaxFailedCount) {
    ejection.failed_count = 0;
    ejection.ejected = true;
    ejection.ejected_at = std::chrono::steady_clock::now();
  }
}

void SeedPeerSelector::OnSuccess(const std::string& endpoint) {
  // Usually there is nothing to clear, so check under the shared lock first.
  {
    std::shared_lock<std::shared_mutex> lock(ejections_mu_);
    if (ejections_.find(endpoint) == ejections_.end()) {
      return;
    }
  }

  std::unique_lock<std::shared_mutex> lock(ejections_mu_);
  ejections_.erase(endpoint);
}

bool SeedPeerSelector::IsEjected(const std::string& endpoint) const {
  std::shared_lock<std::shared_mutex> lock(ejections_mu_);
  const auto it = ejections_.find(endpoint);
  return it != ejections_.end() && it->second.ejected &&
         std::chrono::steady_clock::now() - it->second.ejected_at <
             ejection_cooldown_;
}

absl::StatusOr<std::vector<SeedPeer>> SeedPeerSelector::Select(
    absl::string_view task_id, std::uint32_t replicas) const {
  std::shared_lock<std::shared_mutex> lock(mu_);
  if (hosts_.empty()) {
    return absl::NotFoundError("host not found: seed peers");
  }

  // The number of replicas cannot exceed the total number of seed peers.
  const std::size_t expected_replicas =
      std::min<std::size_t>(replicas, hosts_.size());

  // Walk the ring clockwise from the task position collecting distinct seed
  // peers, growing the window until enough distinct seed peers are found or
  // the whole ring is covered.
  std::vector<const SeedPeer*> replica_seed_peers;
  for (std::size_t window = expected_replicas * 2; expected_replicas > 0;
       window *= 2) {
    replica_seed_peers.clear();
    std::set<std::string> seen;
    const std::vector<std::string> names =
        ring_.GetWithReplicas(task_id, window);
    for (const auto& name : names) {
      if (!seen.insert(name).second) {
        continue;
      }

      const auto it = hosts_.find(name);
      if (it == hosts_.end()) {
        continue;
      }

      replica_seed_peers.push_back(&it->second);
      if (replica_seed_peers.size() == expected_replicas) {
        break;
      }
    }

    if (replica_seed_peers.size() == expected_replicas ||
        names.size() >= ring_.size()) {
      break;
    }
  }

  // Select the healthy replicas, failing when none is.
  std::vector<SeedPeer> seed_peers;
  for (const SeedPeer* seed_peer : replica_seed_peers) {
    if (seed_peer->healthy) {
      seed_peers.push_back(*seed_peer);
    }
  }

  if (seed_peers.empty()) {
    return absl::NotFoundError("host not found: selected seed peers");
  }

  return seed_peers;
}

absl::StatusOr<std::vector<SeedPeer>> SeedPeerSelector::SelectAll() const {
  std::shared_lock<std::shared_mutex> lock(mu_);
  std::vector<SeedPeer> seed_peers;
  for (const auto& host : hosts_) {
    if (host.second.healthy) {
      seed_peers.push_back(host.second);
    }
  }

  if (seed_peers.empty()) {
    return absl::NotFoundError("host not found: seed peers");
  }

  return seed_peers;
}

std::string SeedPeerSelector::ProxyEndpoint(const SeedPeer& seed_peer) {
  return absl::StrCat("http://",
                      JoinHostPort(seed_peer.ip, seed_peer.proxy_port));
}

}  // namespace dragonfly::client_request::internal
