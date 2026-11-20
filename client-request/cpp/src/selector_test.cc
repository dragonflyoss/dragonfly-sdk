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

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <algorithm>
#include <chrono>
#include <set>
#include <string>
#include <thread>
#include <vector>

#include "absl/strings/str_cat.h"

namespace dragonfly::client_request::internal {
namespace {

using ::testing::IsEmpty;
using ::testing::SizeIs;
using ::testing::UnorderedElementsAre;

std::vector<SeedPeer> TestSeedPeers(int count,
                                    const std::string& unhealthy = "") {
  std::vector<SeedPeer> seed_peers;
  for (int i = 1; i <= count; ++i) {
    SeedPeer seed_peer;
    seed_peer.name = absl::StrCat("host-", i);
    seed_peer.hostname = seed_peer.name;
    seed_peer.ip = absl::StrCat("192.168.1.", i);
    seed_peer.port = 8080;
    seed_peer.proxy_port = 8080;
    seed_peer.healthy = seed_peer.name != unhealthy;
    seed_peers.push_back(seed_peer);
  }

  return seed_peers;
}

std::vector<std::string> SelectNames(const SeedPeerSelector& selector,
                                     const std::string& task_id,
                                     std::uint32_t replicas) {
  std::vector<std::string> names;
  auto seed_peers = selector.Select(task_id, replicas);
  EXPECT_TRUE(seed_peers.ok()) << seed_peers.status();
  if (seed_peers.ok()) {
    for (const auto& seed_peer : *seed_peers) {
      names.push_back(seed_peer.name);
    }
  }

  return names;
}

TEST(SeedPeerSelectorTest, SelectFailsWithoutHosts) {
  SeedPeerSelector selector;
  auto seed_peers = selector.Select("test-task", 1);
  EXPECT_EQ(seed_peers.status().code(), absl::StatusCode::kNotFound);
  EXPECT_EQ(seed_peers.status().message(), "host not found: seed peers");
  EXPECT_EQ(selector.SelectAll().status().code(), absl::StatusCode::kNotFound);
}

TEST(SeedPeerSelectorTest, SelectReturnsTheSingleHost) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(1));

  auto seed_peers = selector.Select("test-task", 1);
  ASSERT_TRUE(seed_peers.ok()) << seed_peers.status();
  ASSERT_THAT(*seed_peers, SizeIs(1));
  EXPECT_EQ((*seed_peers)[0].name, "host-1");
  EXPECT_EQ((*seed_peers)[0].ip, "192.168.1.1");
}

TEST(SeedPeerSelectorTest, SelectReturnsDistinctReplicas) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(5));

  for (std::uint32_t replicas : {1U, 2U, 3U, 5U}) {
    std::vector<std::string> names =
        SelectNames(selector, "test-task", replicas);
    EXPECT_THAT(names, SizeIs(replicas));
    EXPECT_THAT(std::set<std::string>(names.begin(), names.end()),
                SizeIs(replicas));
  }
}

TEST(SeedPeerSelectorTest, SelectClampsTheReplicasToTheHosts) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(3));
  EXPECT_THAT(SelectNames(selector, "test-task", 10),
              UnorderedElementsAre("host-1", "host-2", "host-3"));
}

TEST(SeedPeerSelectorTest, SelectAllReturnsEveryHealthyHost) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(5));

  auto seed_peers = selector.SelectAll();
  ASSERT_TRUE(seed_peers.ok()) << seed_peers.status();
  EXPECT_THAT(*seed_peers, SizeIs(5));

  selector.Update(TestSeedPeers(2, "host-2"));
  seed_peers = selector.SelectAll();
  ASSERT_TRUE(seed_peers.ok()) << seed_peers.status();
  ASSERT_THAT(*seed_peers, SizeIs(1));
  EXPECT_EQ((*seed_peers)[0].name, "host-1");

  selector.Update(TestSeedPeers(1, "host-1"));
  EXPECT_EQ(selector.SelectAll().status().code(), absl::StatusCode::kNotFound);
}

TEST(SeedPeerSelectorTest, SelectIsConsistent) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(10));

  std::vector<std::string> names = SelectNames(selector, "consistent-task", 3);
  EXPECT_EQ(SelectNames(selector, "consistent-task", 3), names);

  SeedPeerSelector other;
  other.Update(TestSeedPeers(10));
  EXPECT_EQ(SelectNames(other, "consistent-task", 3), names);
}

TEST(SeedPeerSelectorTest, SelectServesATaskOnlyFromItsHealthyReplicas) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(5));

  std::vector<std::string> task_ids;
  std::vector<std::vector<std::string>> healthy_selections;
  for (int i = 0; i < 100; ++i) {
    task_ids.push_back(absl::StrCat("task-", i));
    healthy_selections.push_back(SelectNames(selector, task_ids.back(), 2));
  }

  selector.Update(TestSeedPeers(5, "host-3"));
  for (std::size_t i = 0; i < task_ids.size(); ++i) {
    std::vector<std::string> expected = healthy_selections[i];
    expected.erase(std::remove(expected.begin(), expected.end(), "host-3"),
                   expected.end());
    auto seed_peers = selector.Select(task_ids[i], 2);
    if (expected.empty()) {
      EXPECT_EQ(seed_peers.status().code(), absl::StatusCode::kNotFound);
      continue;
    }

    EXPECT_EQ(SelectNames(selector, task_ids[i], 2), expected) << task_ids[i];
  }

  selector.Update(TestSeedPeers(5));
  for (std::size_t i = 0; i < task_ids.size(); ++i) {
    EXPECT_EQ(SelectNames(selector, task_ids[i], 2), healthy_selections[i]);
  }
}

TEST(SeedPeerSelectorTest, SelectFailsWhenEveryReplicaIsUnhealthy) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(5));
  std::vector<std::string> replica = SelectNames(selector, "test-task", 1);
  ASSERT_THAT(replica, SizeIs(1));

  selector.Update(TestSeedPeers(5, replica[0]));
  auto seed_peers = selector.Select("test-task", 1);
  EXPECT_EQ(seed_peers.status().code(), absl::StatusCode::kNotFound);
  EXPECT_EQ(seed_peers.status().message(),
            "host not found: selected seed peers");
}

TEST(SeedPeerSelectorTest, OnFailureEjectsAProxyAfterConsecutiveFailures) {
  SeedPeerSelector selector;
  const std::string endpoint = "http://192.168.1.1:8080";

  for (int i = 0; i < kMaxFailedCount - 1; ++i) {
    selector.OnFailure(endpoint);
  }
  EXPECT_FALSE(selector.IsEjected(endpoint));

  selector.OnSuccess(endpoint);
  for (int i = 0; i < kMaxFailedCount - 1; ++i) {
    selector.OnFailure(endpoint);
  }
  EXPECT_FALSE(selector.IsEjected(endpoint));

  selector.OnFailure(endpoint);
  EXPECT_TRUE(selector.IsEjected(endpoint));
  EXPECT_FALSE(selector.IsEjected("http://192.168.1.2:8080"));

  selector.OnSuccess(endpoint);
  EXPECT_FALSE(selector.IsEjected(endpoint));

  for (int i = 0; i < kMaxFailedCount; ++i) {
    selector.OnFailure(endpoint);
  }
  EXPECT_TRUE(selector.IsEjected(endpoint));
}

TEST(SeedPeerSelectorTest, AnEjectedProxyIsBackOnceTheCooldownPassed) {
  SeedPeerSelector selector(std::chrono::milliseconds(0));
  const std::string endpoint = "http://192.168.1.1:8080";
  for (int i = 0; i < kMaxFailedCount; ++i) {
    selector.OnFailure(endpoint);
  }

  EXPECT_FALSE(selector.IsEjected(endpoint));
}

TEST(SeedPeerSelectorTest, ProxyEndpointJoinsTheIpAndTheProxyPort) {
  SeedPeer seed_peer;
  seed_peer.ip = "127.0.0.1";
  seed_peer.proxy_port = 4001;
  EXPECT_EQ(SeedPeerSelector::ProxyEndpoint(seed_peer),
            "http://127.0.0.1:4001");

  seed_peer.ip = "2001:db8::1";
  EXPECT_EQ(SeedPeerSelector::ProxyEndpoint(seed_peer),
            "http://[2001:db8::1]:4001");
}

TEST(SeedPeerSelectorTest, ConcurrentSelectAndUpdate) {
  SeedPeerSelector selector;
  selector.Update(TestSeedPeers(5));

  std::vector<std::thread> threads;
  threads.reserve(8);
  for (int i = 0; i < 8; ++i) {
    threads.emplace_back([&selector, i] {
      for (int j = 0; j < 50; ++j) {
        if (i % 2 == 0) {
          selector.Update(TestSeedPeers(5 + j % 2));
        } else {
          EXPECT_THAT(
              SelectNames(selector, absl::StrCat("concurrent-task-", j), 2),
              SizeIs(2));
        }
      }
    });
  }

  for (auto& thread : threads) {
    thread.join();
  }

  EXPECT_THAT(SelectNames(selector, "test-task", 1), ::testing::Not(IsEmpty()));
}

}  // namespace
}  // namespace dragonfly::client_request::internal
