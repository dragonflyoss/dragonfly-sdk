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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_SELECTOR_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_SELECTOR_H_

#include <chrono>
#include <condition_variable>
#include <cstdint>
#include <map>
#include <memory>
#include <mutex>
#include <shared_mutex>
#include <string>
#include <thread>
#include <unordered_map>
#include <vector>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"
#include "dragonfly/client_request/proxy.h"
#include "src/hashring.h"
#include "src/scheduler.h"

namespace dragonfly::client_request::internal {

/// The consecutive failures to connect to a seed peer proxy before it is
/// ejected.
inline constexpr int kMaxFailedCount = 5;

/// Selects seed peers with a consistent hash ring. It is thread-safe.
///
/// The ring holds every seed peer, and a task is served only by its healthy
/// replicas:
///
///     task ──▶ ring position ──▶ replicas ─┬─ seed-peer-1 ──unhealthy──▶ skip
///                                          └─ seed-peer-2 ──healthy────▶ pick
class SeedPeerSelector {
 public:
  /// Creates a selector without any seed peer, an ejected proxy staying out
  /// for the cooldown.
  explicit SeedPeerSelector(
      std::chrono::milliseconds ejection_cooldown = std::chrono::seconds(30));

  /// Stops the background refresh.
  ~SeedPeerSelector();
  SeedPeerSelector(const SeedPeerSelector&) = delete;
  SeedPeerSelector& operator=(const SeedPeerSelector&) = delete;

  /// Refreshes the seed peers from the scheduler, doing nothing while another
  /// refresh is running.
  absl::Status Refresh(Scheduler& scheduler);

  /// Refreshes the seed peers from the scheduler every interval on a
  /// background thread until Close.
  void Run(std::shared_ptr<Scheduler> scheduler,
           std::chrono::milliseconds health_check_interval);

  /// Stops the background refresh.
  void Close();

  /// Replaces the seed peers and their health, rebuilding the ring only when
  /// their names change.
  void Update(const std::vector<SeedPeer>& seed_peers);

  /// Records a failure to connect to the proxy at the endpoint, ejecting it
  /// after kMaxFailedCount in a row.
  void OnFailure(const std::string& endpoint);

  /// Records an answer of the proxy at the endpoint, clearing its failures.
  void OnSuccess(const std::string& endpoint);

  /// Reports whether the proxy at the endpoint is ejected.
  bool IsEjected(const std::string& endpoint) const;

  /// Selects the healthy replicas of the task in the consistent hash ring
  /// order, up to replicas of them.
  absl::StatusOr<std::vector<SeedPeer>> Select(absl::string_view task_id,
                                               std::uint32_t replicas) const;

  /// Selects every healthy seed peer.
  absl::StatusOr<std::vector<SeedPeer>> SelectAll() const;

  /// Returns the endpoint of the proxy of a seed peer, such as
  /// "http://127.0.0.1:4001".
  static std::string ProxyEndpoint(const SeedPeer& seed_peer);

 private:
  /// The ejection state of a seed peer proxy.
  struct Ejection {
    /// The count of consecutive failures to connect.
    int failed_count = 0;

    /// Whether the proxy has been ejected, and when it last was.
    bool ejected = false;
    std::chrono::steady_clock::time_point ejected_at;
  };

  const std::chrono::milliseconds ejection_cooldown_;

  /// Protects hosts_ and ring_.
  mutable std::shared_mutex mu_;
  std::map<std::string, SeedPeer> hosts_;
  VNodeHashRing ring_;

  /// Protects ejections_.
  mutable std::shared_mutex ejections_mu_;
  std::unordered_map<std::string, Ejection> ejections_;

  /// Held by the refresh that is running.
  std::mutex refresh_mu_;

  /// Protects closed_, which ends the background thread.
  std::mutex close_mu_;
  std::condition_variable close_cv_;
  bool closed_ = false;
  std::thread thread_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_SELECTOR_H_
