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

#include <gmock/gmock.h>
#include <gtest/gtest.h>

namespace dragonfly::client_request::internal {
namespace {

using ::testing::ElementsAre;
using ::testing::ElementsAreArray;
using ::testing::IsEmpty;
using ::testing::SizeIs;

VNodeHashRing NewRing(int vnodes_per_node,
                      const std::vector<std::string>& nodes) {
  VNodeHashRing ring(vnodes_per_node);
  for (const auto& name : nodes) {
    ring.Add(name);
  }

  return ring;
}

TEST(VNodeHashRingTest, AddInsertsTheVirtualNodesOfANode) {
  struct TestCase {
    int vnodes_per_node;
    std::vector<std::string> nodes;
    std::size_t size;
  };
  const TestCase test_cases[] = {
      {3, {}, 0},
      {kDefaultVNodesPerNode, {"default-seed-client-1"}, 512},
      {2, {"default-seed-client-1"}, 2},
      {2, {"default-seed-client-1", "default-seed-client-2"}, 4},
  };

  for (const auto& test_case : test_cases) {
    VNodeHashRing ring = NewRing(test_case.vnodes_per_node, test_case.nodes);
    EXPECT_EQ(ring.size(), test_case.size);
    EXPECT_EQ(ring.empty(), test_case.size == 0);
  }

  VNodeHashRing ring;
  ring.Add("default-seed-client-1");
  EXPECT_EQ(ring.size(), 512U);
}

TEST(VNodeHashRingTest, GetWithReplicasIsEmptyOnAnEmptyRing) {
  VNodeHashRing ring(2);
  EXPECT_THAT(ring.GetWithReplicas("task", 0), IsEmpty());
  EXPECT_THAT(ring.GetWithReplicas("task", 2), IsEmpty());
}

TEST(VNodeHashRingTest, GetWithReplicasGivesEveryKeyToASingleNode) {
  VNodeHashRing ring = NewRing(2, {"default-seed-client-1"});
  for (const char* key : {"", "task-a", "task-b"}) {
    EXPECT_THAT(ring.GetWithReplicas(key, 0),
                ElementsAre("default-seed-client-1"));
  }
}

TEST(VNodeHashRingTest, GetWithReplicasMapsAKeyToTheSameVirtualNode) {
  VNodeHashRing ring =
      NewRing(2, {"default-seed-client-1", "default-seed-client-2",
                  "default-seed-client-3"});
  EXPECT_EQ(ring.GetWithReplicas("task", 0), ring.GetWithReplicas("task", 0));
}

TEST(VNodeHashRingTest, GetWithReplicasShrinksToTheWholeRing) {
  VNodeHashRing ring =
      NewRing(2, {"default-seed-client-1", "default-seed-client-2"});
  EXPECT_THAT(ring.GetWithReplicas("task", 2), SizeIs(3));
  EXPECT_THAT(ring.GetWithReplicas("task", 100), SizeIs(4));
}

TEST(VNodeHashRingTest, AddOrderDoesNotAffectTheNodeOfAKey) {
  const std::vector<std::string> nodes = {"default-seed-client-1",
                                          "default-seed-client-2",
                                          "default-seed-client-3"};
  VNodeHashRing ring_a = NewRing(kDefaultVNodesPerNode, nodes);
  VNodeHashRing ring_b =
      NewRing(kDefaultVNodesPerNode, {nodes.rbegin(), nodes.rend()});

  for (const char* key : {"task-a", "task-b", "task-c", "task-d", "task-e"}) {
    EXPECT_EQ(ring_a.GetWithReplicas(key, 0), ring_b.GetWithReplicas(key, 0));
  }
}

TEST(VNodeHashRingTest, GetWithReplicasMatchesTheGoAndRustRings) {
  const std::vector<std::string> seed_peers = {"seed-peer-1", "seed-peer-2",
                                               "seed-peer-3"};
  const std::vector<std::string> seed_clients = {"default-seed-client-0",
                                                 "default-seed-client-1",
                                                 "default-seed-client-2"};
  struct TestCase {
    int vnodes_per_node;
    std::vector<std::string> nodes;
    std::string key;
    std::size_t replicas;
    std::vector<std::string> expected;
  };
  const TestCase test_cases[] = {
      {3,
       seed_peers,
       "task-a",
       2,
       {"seed-peer-3", "seed-peer-1", "seed-peer-3"}},
      {3,
       seed_peers,
       "task-b",
       2,
       {"seed-peer-2", "seed-peer-2", "seed-peer-1"}},
      {3,
       seed_peers,
       "task-c",
       2,
       {"seed-peer-2", "seed-peer-1", "seed-peer-3"}},
      {3,
       seed_peers,
       "task-a",
       100,
       {"seed-peer-3", "seed-peer-1", "seed-peer-3", "seed-peer-1",
        "seed-peer-2", "seed-peer-3", "seed-peer-2", "seed-peer-2",
        "seed-peer-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000000000",
       2,
       {"default-seed-client-1", "default-seed-client-0",
        "default-seed-client-0"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000001eef",
       2,
       {"default-seed-client-1", "default-seed-client-2",
        "default-seed-client-0"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000003dde",
       2,
       {"default-seed-client-2", "default-seed-client-0",
        "default-seed-client-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000005ccd",
       2,
       {"default-seed-client-0", "default-seed-client-0",
        "default-seed-client-2"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000007bbc",
       2,
       {"default-seed-client-0", "default-seed-client-1",
        "default-seed-client-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000009aab",
       2,
       {"default-seed-client-1", "default-seed-client-0",
        "default-seed-client-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "000000000000000000000000000000000000000000000000000000000000b99a",
       2,
       {"default-seed-client-0", "default-seed-client-0",
        "default-seed-client-2"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "000000000000000000000000000000000000000000000000000000000000d889",
       2,
       {"default-seed-client-1", "default-seed-client-2",
        "default-seed-client-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "000000000000000000000000000000000000000000000000000000000000f778",
       2,
       {"default-seed-client-2", "default-seed-client-1",
        "default-seed-client-0"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000011667",
       2,
       {"default-seed-client-0", "default-seed-client-1",
        "default-seed-client-2"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000013556",
       2,
       {"default-seed-client-2", "default-seed-client-2",
        "default-seed-client-0"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "0000000000000000000000000000000000000000000000000000000000015445",
       2,
       {"default-seed-client-2", "default-seed-client-1",
        "default-seed-client-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "",
       2,
       {"default-seed-client-1", "default-seed-client-2",
        "default-seed-client-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "a",
       2,
       {"default-seed-client-0", "default-seed-client-1",
        "default-seed-client-0"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "00000",
       2,
       {"default-seed-client-2", "default-seed-client-2",
        "default-seed-client-1"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "dragonfly",
       2,
       {"default-seed-client-1", "default-seed-client-0",
        "default-seed-client-2"}},
      {kDefaultVNodesPerNode,
       seed_clients,
       "dragonfly-seed-client-0",
       2,
       {"default-seed-client-2", "default-seed-client-0",
        "default-seed-client-0"}},
  };

  for (const auto& test_case : test_cases) {
    VNodeHashRing ring = NewRing(test_case.vnodes_per_node, test_case.nodes);
    EXPECT_THAT(ring.GetWithReplicas(test_case.key, test_case.replicas),
                ElementsAreArray(test_case.expected))
        << test_case.key;
    EXPECT_THAT(ring.GetWithReplicas(test_case.key, 0),
                ElementsAre(test_case.expected[0]))
        << test_case.key;
  }
}

}  // namespace
}  // namespace dragonfly::client_request::internal
