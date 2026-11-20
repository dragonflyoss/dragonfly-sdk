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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_HASHRING_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_HASHRING_H_

#include <cstddef>
#include <cstdint>
#include <string>
#include <vector>

#include "absl/strings/string_view.h"

namespace dragonfly::client_request::internal {

/// The default number of virtual nodes of each node on the ring.
inline constexpr int kDefaultVNodesPerNode = 512;

/// A consistent hash ring with virtual nodes, placing the keys as the Go
/// d7y.io/hashring and the Rust dragonfly-hashring do. It is not thread-safe.
class VNodeHashRing {
 public:
  /// Creates an empty ring with the number of virtual nodes of each node.
  explicit VNodeHashRing(int vnodes_per_node = kDefaultVNodesPerNode);

  /// Adds the virtual nodes of the named node to the ring.
  void Add(absl::string_view name);

  /// Returns the names of the nodes of the virtual node responsible for the
  /// key and of the next `replicas` virtual nodes clockwise, empty when the
  /// ring is empty.
  std::vector<std::string> GetWithReplicas(absl::string_view key,
                                           std::size_t replicas) const;

  /// Returns the number of virtual nodes on the ring.
  std::size_t size() const;

  /// Reports whether the ring has no virtual nodes.
  bool empty() const;

 private:
  /// A virtual node on the ring.
  struct VNode {
    /// The position of the virtual node on the ring.
    std::uint64_t key;

    /// The index of the name of its node in `names_`.
    std::size_t name;
  };

  /// The number of virtual nodes of each node.
  int vnodes_per_node_;

  /// The names of the nodes.
  std::vector<std::string> names_;

  /// The ring sorted by key.
  std::vector<VNode> vnodes_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_HASHRING_H_
