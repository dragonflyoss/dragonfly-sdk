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

#include "src/hashring.h"

#include <algorithm>

#include "absl/strings/str_cat.h"

namespace dragonfly::client_request::internal {
namespace {

std::uint64_t RotateLeft(std::uint64_t value, int bits) {
  return (value << bits) | (value >> (64 - bits));
}

// Hashes the data with SipHash-2-4 and zero keys, the hasher of the Rust std
// Hash encoding the positions on the ring are defined by. Changing it moves
// every key to another node.
std::uint64_t SipHash(absl::string_view data) {
  std::uint64_t v0 = 0x736f6d6570736575;
  std::uint64_t v1 = 0x646f72616e646f6d;
  std::uint64_t v2 = 0x6c7967656e657261;
  std::uint64_t v3 = 0x7465646279746573;
  auto round = [&] {
    v0 += v1;
    v1 = RotateLeft(v1, 13) ^ v0;
    v0 = RotateLeft(v0, 32);
    v2 += v3;
    v3 = RotateLeft(v3, 16) ^ v2;
    v0 += v3;
    v3 = RotateLeft(v3, 21) ^ v0;
    v2 += v1;
    v1 = RotateLeft(v1, 17) ^ v2;
    v2 = RotateLeft(v2, 32);
  };
  auto compress = [&](std::uint64_t word) {
    v3 ^= word;
    round();
    round();
    v0 ^= word;
  };

  // Compress the little-endian words, the last one ending with the length.
  std::uint64_t word = 0;
  for (std::size_t i = 0; i < data.size(); ++i) {
    word |= std::uint64_t{static_cast<unsigned char>(data[i])} << (8 * (i % 8));
    if (i % 8 == 7) {
      compress(word);
      word = 0;
    }
  }
  compress(word | (static_cast<std::uint64_t>(data.size()) << 56));

  v2 ^= 0xff;
  for (int i = 0; i < 4; ++i) {
    round();
  }

  return v0 ^ v1 ^ v2 ^ v3;
}

}  // namespace

VNodeHashRing::VNodeHashRing(int vnodes_per_node)
    : vnodes_per_node_(vnodes_per_node) {}

void VNodeHashRing::Add(absl::string_view name) {
  // Hash the Rust std Hash encoding of a virtual node: its id as 8
  // little-endian bytes, the name and the 0xff terminator of a string.
  const std::size_t size = vnodes_.size();
  for (int id = 0; id < vnodes_per_node_; ++id) {
    char encoded[8];
    for (int i = 0; i < 8; ++i) {
      encoded[i] = static_cast<char>(static_cast<std::uint64_t>(id) >> (8 * i));
    }

    vnodes_.push_back(
        {SipHash(absl::StrCat(absl::string_view(encoded, 8), name, "\xff")),
         names_.size()});
  }
  names_.emplace_back(name);

  // Merge the new virtual nodes into the ring, keeping the existing ones
  // before the new ones with the same key.
  auto less = [](const VNode& a, const VNode& b) { return a.key < b.key; };
  std::sort(vnodes_.begin() + size, vnodes_.end(), less);
  std::inplace_merge(vnodes_.begin(), vnodes_.begin() + size, vnodes_.end(),
                     less);
}

std::vector<std::string> VNodeHashRing::GetWithReplicas(
    absl::string_view key, std::size_t replicas) const {
  std::vector<std::string> names;
  if (vnodes_.empty()) {
    return names;
  }

  // Start at the first virtual node whose key is not less than the key,
  // hashed with the Rust std Hash encoding of a string.
  const std::uint64_t hash = SipHash(absl::StrCat(key, "\xff"));
  const std::size_t start =
      std::lower_bound(vnodes_.begin(), vnodes_.end(), hash,
                       [](const VNode& vnode, std::uint64_t hash) {
                         return vnode.key < hash;
                       }) -
      vnodes_.begin();

  // Shrink to the whole ring when the replicas are more than its length.
  const std::size_t take =
      replicas > vnodes_.size() ? vnodes_.size() : replicas + 1;
  names.reserve(take);
  for (std::size_t i = 0; i < take; ++i) {
    names.push_back(names_[vnodes_[(start + i) % vnodes_.size()].name]);
  }

  return names;
}

std::size_t VNodeHashRing::size() const { return vnodes_.size(); }

bool VNodeHashRing::empty() const { return vnodes_.empty(); }

}  // namespace dragonfly::client_request::internal
