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

#include "src/scheduler.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <chrono>
#include <string>
#include <utility>
#include <vector>

#include "src/net.h"
#include "src/selector.h"
#include "src/testing/fake_servers.h"

namespace dragonfly::client_request::internal {
namespace {

using testing::FakeScheduler;
using testing::FakeSeedPeer;
using ::testing::SizeIs;

TEST(NetTest, JoinHostPortBracketsIpv6) {
  const std::pair<std::string, std::string> test_cases[] = {
      {"127.0.0.1", "127.0.0.1:80"},
      {"192.168.1.1", "192.168.1.1:80"},
      {"::1", "[::1]:80"},
      {"2001:db8::1", "[2001:db8::1]:80"},
  };

  for (const auto& [ip, expected] : test_cases) {
    EXPECT_EQ(JoinHostPort(ip, 80), expected);
  }
}

TEST(SchedulerTest, SchedulerTargetIsTheAuthorityOfTheEndpoint) {
  const std::pair<std::string, std::string> test_cases[] = {
      {"http://127.0.0.1:8002", "127.0.0.1:8002"},
      {"http://scheduler-service:8002/", "scheduler-service:8002"},
      {"https://scheduler.example.com:8002/path?query",
       "scheduler.example.com:8002"},
      {"127.0.0.1:8002", ""},
      {"http://", ""},
      {"", ""},
  };

  for (const auto& [endpoint, expected] : test_cases) {
    auto target = SchedulerTarget(endpoint);
    if (expected.empty()) {
      EXPECT_EQ(target.status().code(), absl::StatusCode::kInvalidArgument)
          << endpoint;
    } else {
      ASSERT_TRUE(target.ok()) << target.status();
      EXPECT_EQ(*target, expected);
    }
  }
}

TEST(SchedulerTest, ListSeedPeersChecksTheHealthOfEveryHost) {
  FakeSeedPeer serving("seed-peer-1");
  FakeSeedPeer not_serving("seed-peer-2", false);
  common::v2::Host unreachable;
  unreachable.set_name("seed-peer-3");
  unreachable.set_hostname("seed-peer-3");
  unreachable.set_ip("127.0.0.1");
  unreachable.set_port(1);
  unreachable.set_proxy_port(4001);
  FakeScheduler fake_scheduler(
      {serving.host(), not_serving.host(), unreachable});

  Scheduler scheduler(fake_scheduler.target(), std::chrono::seconds(5));
  auto seed_peers = scheduler.ListSeedPeers();
  ASSERT_TRUE(seed_peers.ok()) << seed_peers.status();
  ASSERT_THAT(*seed_peers, SizeIs(3));
  EXPECT_EQ((*seed_peers)[0].name, "seed-peer-1");
  EXPECT_EQ((*seed_peers)[0].hostname, "seed-peer-1");
  EXPECT_EQ((*seed_peers)[0].ip, "127.0.0.1");
  EXPECT_EQ((*seed_peers)[0].port, serving.seed_peer().port);
  EXPECT_TRUE((*seed_peers)[0].healthy);
  EXPECT_FALSE((*seed_peers)[1].healthy);
  EXPECT_FALSE((*seed_peers)[2].healthy);
  EXPECT_EQ((*seed_peers)[2].proxy_port, 4001);
  EXPECT_EQ(fake_scheduler.list_hosts_request().type(), 1U);
}

TEST(SchedulerTest, ListSeedPeersKeepsTheCodeOfAFailure) {
  FakeScheduler fake_scheduler({});
  fake_scheduler.list_hosts = [] {
    return grpc::Status(grpc::StatusCode::PERMISSION_DENIED, "denied");
  };

  Scheduler scheduler(fake_scheduler.target(), std::chrono::seconds(5));
  EXPECT_EQ(scheduler.ListSeedPeers().status().code(),
            absl::StatusCode::kPermissionDenied);

  Scheduler unreachable("127.0.0.1:1", std::chrono::milliseconds(500));
  EXPECT_FALSE(unreachable.ListSeedPeers().ok());
}

TEST(SchedulerTest, SchedulerClientListsTheSeedPeers) {
  EXPECT_EQ(SchedulerClient::Create("127.0.0.1:8002", std::chrono::seconds(5))
                .status()
                .code(),
            absl::StatusCode::kInvalidArgument);

  FakeSeedPeer seed_peer("seed-peer-1");
  FakeScheduler fake_scheduler({seed_peer.host()});
  auto client = SchedulerClient::Create(fake_scheduler.endpoint(),
                                        std::chrono::seconds(5));
  ASSERT_TRUE(client.ok()) << client.status();

  auto seed_peers = (*client)->ListSeedPeers();
  ASSERT_TRUE(seed_peers.ok()) << seed_peers.status();
  ASSERT_THAT(*seed_peers, SizeIs(1));
  EXPECT_TRUE((*seed_peers)[0].healthy);
}

TEST(SchedulerTest, SelectorRefreshesTheSeedPeersFromTheScheduler) {
  FakeSeedPeer first("seed-peer-1");
  FakeSeedPeer second("seed-peer-2");
  FakeScheduler fake_scheduler({first.host()});
  auto scheduler = std::make_shared<Scheduler>(fake_scheduler.target(),
                                               std::chrono::seconds(5));

  SeedPeerSelector selector;
  ASSERT_TRUE(selector.Refresh(*scheduler).ok());
  auto seed_peers = selector.SelectAll();
  ASSERT_TRUE(seed_peers.ok()) << seed_peers.status();
  EXPECT_THAT(*seed_peers, SizeIs(1));

  fake_scheduler.set_hosts({first.host(), second.host()});
  selector.Run(scheduler, std::chrono::milliseconds(20));
  for (int i = 0; i < 200 && selector.SelectAll()->size() < 2; ++i) {
    std::this_thread::sleep_for(std::chrono::milliseconds(10));
  }
  EXPECT_THAT(*selector.SelectAll(), SizeIs(2));

  selector.Close();
  const int calls = fake_scheduler.list_hosts_calls;
  std::this_thread::sleep_for(std::chrono::milliseconds(60));
  EXPECT_EQ(fake_scheduler.list_hosts_calls, calls);
}

}  // namespace
}  // namespace dragonfly::client_request::internal
