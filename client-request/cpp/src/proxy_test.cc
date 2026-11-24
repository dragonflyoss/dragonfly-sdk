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

#include "dragonfly/client_request/proxy.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <atomic>
#include <chrono>
#include <memory>
#include <sstream>
#include <string>
#include <utility>
#include <vector>

#include "dragonfly/client_request/proxy_with_seed_peers.h"
#include "src/testing/fake_servers.h"
#include "src/testing/http_server.h"

namespace dragonfly::client_request {
namespace {

using internal::testing::FakeScheduler;
using internal::testing::FakeSeedPeer;
using internal::testing::HeaderValue;
using internal::testing::HttpServer;
using internal::testing::TestRequest;
using internal::testing::TestResponse;
using std::chrono::milliseconds;
using ::testing::ElementsAre;
using ::testing::HasSubstr;
using ::testing::IsSubsetOf;
using ::testing::SizeIs;

constexpr char kUrl[] = "http://example.com/file.txt";
constexpr char kBody[] = "hello dragonfly";

GetRequest TestGetRequest() {
  GetRequest request;
  request.url = kUrl;
  request.timeout = std::chrono::seconds(10);
  return request;
}

TestResponse ServeBody(const TestRequest&) {
  TestResponse response;
  response.body = kBody;
  return response;
}

std::string ReadAll(Body* body) {
  std::string content;
  char buffer[8];
  for (;;) {
    auto n = body->Read(buffer, sizeof(buffer));
    if (!n.ok() || *n == 0) {
      return content;
    }

    content.append(buffer, *n);
  }
}

class ProxyTest : public ::testing::Test {
 protected:
  void Start(int count, const HttpServer::Handler& handler,
             bool first_proxy_refuses = false) {
    std::vector<common::v2::Host> hosts;
    for (int i = 1; i <= count; ++i) {
      seed_peers_.push_back(
          std::make_unique<FakeSeedPeer>("seed-peer-" + std::to_string(i)));
      proxies_.push_back(std::make_unique<HttpServer>(handler));
      common::v2::Host host = seed_peers_.back()->host();
      host.set_proxy_port(
          i == 1 && first_proxy_refuses ? 1 : proxies_.back()->port());
      hosts.push_back(host);
    }

    scheduler_ = std::make_unique<FakeScheduler>(std::move(hosts));
  }

  std::vector<std::string> ProxyEndpoints() const {
    std::vector<std::string> endpoints;
    endpoints.reserve(proxies_.size());
    for (const auto& proxy : proxies_) {
      endpoints.push_back(proxy->endpoint());
    }

    return endpoints;
  }

  int ProxyRequests() const {
    int requests = 0;
    for (const auto& proxy : proxies_) {
      requests += proxy->requests();
    }

    return requests;
  }

  std::vector<std::unique_ptr<FakeSeedPeer>> seed_peers_;
  std::vector<std::unique_ptr<HttpServer>> proxies_;
  std::unique_ptr<FakeScheduler> scheduler_;
};

TEST_F(ProxyTest, CreateValidatesTheOptionsAndTheEndpoint) {
  struct TestCase {
    ProxyOptions options;
    std::string expected_message;
  };

  std::vector<TestCase> test_cases(4);
  test_cases[0].options.scheduler_request_timeout = milliseconds(99);
  test_cases[0].expected_message =
      "scheduler request timeout must be at least 100 milliseconds";
  test_cases[1].options.health_check_interval = milliseconds(999);
  test_cases[1].expected_message =
      "health check interval must be between 1 and 600 seconds";
  test_cases[2].options.read_timeout = milliseconds(0);
  test_cases[2].expected_message = "read timeout must be greater than 0";
  test_cases[3].options.max_retries = 11;
  test_cases[3].expected_message = "max retries must be between 0 and 10";

  for (const auto& test_case : test_cases) {
    auto proxy = Proxy::Create("http://127.0.0.1:8002", test_case.options);
    EXPECT_EQ(proxy.status().code(), absl::StatusCode::kInvalidArgument);
    EXPECT_EQ(proxy.status().message(), test_case.expected_message);
  }

  EXPECT_EQ(Proxy::Create("127.0.0.1:8002").status().code(),
            absl::StatusCode::kInvalidArgument);

  ProxyOptions options;
  options.scheduler_request_timeout = milliseconds(300);
  auto proxy = Proxy::Create("http://127.0.0.1:1", options);
  EXPECT_EQ(proxy.status().code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(proxy.status().message()),
              HasSubstr("failed to create seed peer selector"));
}

TEST_F(ProxyTest, GetServesTheRequestViaASeedPeer) {
  Start(3, ServeBody);
  auto proxy = Proxy::Create(scheduler_->endpoint());
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  GetRequest request = TestGetRequest();
  request.tag = "tag";
  auto response = (*proxy)->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_TRUE(response->success);
  EXPECT_EQ(response->status_code, 200);
  EXPECT_EQ(ReadAll(response->body.get()), kBody);
  ASSERT_EQ(ProxyRequests(), 1);
  for (const auto& seed_peer_proxy : proxies_) {
    if (seed_peer_proxy->requests() == 1) {
      const TestRequest received = seed_peer_proxy->received()[0];
      EXPECT_EQ(received.target, kUrl);
      EXPECT_EQ(HeaderValue(received.header, "x-dragonfly-tag"), "tag");
      EXPECT_EQ(HeaderValue(received.header, "x-dragonfly-use-p2p"), "true");
    }
  }

  std::ostringstream output;
  response = (*proxy)->GetInto(request, output);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(response->body, nullptr);
  EXPECT_EQ(output.str(), kBody);

  request.url = "ftp://example.com/file.txt";
  EXPECT_EQ((*proxy)->Get(request).status().code(),
            absl::StatusCode::kInvalidArgument);
}

TEST_F(ProxyTest, LookupEndpointsReturnsTheReplicasOfTheTask) {
  Start(3, ServeBody);
  auto proxy = Proxy::Create(scheduler_->endpoint());
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  GetRequest request = TestGetRequest();
  auto endpoints = (*proxy)->LookupEndpoints(request);
  ASSERT_TRUE(endpoints.ok()) << endpoints.status();
  EXPECT_THAT(*endpoints, SizeIs(2));
  EXPECT_THAT(*endpoints, IsSubsetOf(ProxyEndpoints()));
  EXPECT_EQ(*(*proxy)->LookupEndpoints(request), *endpoints);

  request.replicas = 5;
  endpoints = (*proxy)->LookupEndpoints(request);
  ASSERT_TRUE(endpoints.ok()) << endpoints.status();
  EXPECT_THAT(*endpoints, SizeIs(3));

  request.replicas = 0;
  EXPECT_EQ((*proxy)->LookupEndpoints(request).status().code(),
            absl::StatusCode::kInvalidArgument);
}

TEST_F(ProxyTest, GetRetriesATransientFailureOnTheNextSeedPeer) {
  std::atomic<int> requests{0};
  Start(2, [&requests](const TestRequest& request) {
    if (++requests == 1) {
      TestResponse response;
      response.status_code = 503;
      response.header = {{"X-Dragonfly-Error-Type", "proxy"}};
      return response;
    }

    return ServeBody(request);
  });

  auto proxy = Proxy::Create(scheduler_->endpoint());
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  auto response = (*proxy)->Get(TestGetRequest());
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(ReadAll(response->body.get()), kBody);
  EXPECT_EQ(proxies_[0]->requests(), 1);
  EXPECT_EQ(proxies_[1]->requests(), 1);
}

TEST_F(ProxyTest, GetEjectsAProxyThatKeepsRefusingTheConnection) {
  Start(2, ServeBody, true);
  auto proxy = Proxy::Create(scheduler_->endpoint());
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  GetRequest request = TestGetRequest();
  ASSERT_THAT(*(*proxy)->LookupEndpoints(request), SizeIs(2));
  for (int i = 0; i < 200; ++i) {
    auto response = (*proxy)->Get(request);
    ASSERT_TRUE(response.ok()) << response.status();
    EXPECT_EQ(ReadAll(response->body.get()), kBody);
    if ((*proxy)->LookupEndpoints(request)->size() == 1) {
      break;
    }
  }

  EXPECT_THAT(*(*proxy)->LookupEndpoints(request),
              ElementsAre(proxies_[1]->endpoint()));
}

TEST_F(ProxyTest, PreheatAndDeleteAddressTheSeedPeers) {
  Start(3, ServeBody);
  auto proxy = Proxy::Create(scheduler_->endpoint());
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  PreheatRequest preheat_request;
  preheat_request.url = kUrl;
  preheat_request.timeout = std::chrono::seconds(5);
  ASSERT_TRUE((*proxy)->Preheat(preheat_request).ok());

  DeleteRequest delete_request;
  delete_request.url = kUrl;
  delete_request.timeout = std::chrono::seconds(5);
  ASSERT_TRUE((*proxy)->Delete(delete_request).ok());

  int download_task_calls = 0;
  int delete_task_calls = 0;
  for (const auto& seed_peer : seed_peers_) {
    EXPECT_EQ(seed_peer->download_task_calls, seed_peer->delete_task_calls);
    download_task_calls += seed_peer->download_task_calls;
    delete_task_calls += seed_peer->delete_task_calls;
  }
  EXPECT_EQ(download_task_calls, 2);
  EXPECT_EQ(delete_task_calls, 2);

  DeleteImageRequest delete_image_request;
  delete_image_request.image = "registry.example.com/library/nginx:latest";
  delete_image_request.timeout = std::chrono::seconds(5);
  auto response = (*proxy)->DeleteImage(delete_image_request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_THAT(response->peers, SizeIs(3));
}

TEST_F(ProxyTest, ABodyOutlivesTheProxy) {
  Start(1, ServeBody);
  auto proxy = Proxy::Create(scheduler_->endpoint());
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  GetRequest request = TestGetRequest();
  request.replicas = 1;
  auto response = (*proxy)->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  proxy->reset();
  EXPECT_EQ(ReadAll(response->body.get()), kBody);
}

TEST_F(ProxyTest, ProxyWithSeedPeersValidatesTheSeedPeersAndTheOptions) {
  SeedPeer seed_peer;
  seed_peer.name = "seed-peer-1";
  seed_peer.healthy = true;
  SeedPeer nameless;

  auto proxy = ProxyWithSeedPeers::Create({});
  EXPECT_EQ(proxy.status().code(), absl::StatusCode::kInvalidArgument);
  EXPECT_EQ(proxy.status().message(), "seed peers must not be empty");

  proxy = ProxyWithSeedPeers::Create({nameless});
  EXPECT_EQ(proxy.status().message(), "seed peer name must not be empty");

  proxy = ProxyWithSeedPeers::Create({seed_peer, seed_peer});
  EXPECT_EQ(proxy.status().message(),
            "seed peer name \"seed-peer-1\" is duplicated");

  ProxyWithSeedPeersOptions options;
  options.read_timeout = milliseconds(0);
  proxy = ProxyWithSeedPeers::Create({seed_peer}, options);
  EXPECT_EQ(proxy.status().message(), "read timeout must be greater than 0");

  options = ProxyWithSeedPeersOptions();
  options.max_retries = 11;
  proxy = ProxyWithSeedPeers::Create({seed_peer}, options);
  EXPECT_EQ(proxy.status().message(), "max retries must be between 0 and 10");
}

TEST_F(ProxyTest, ProxyWithSeedPeersServesTheRequestViaTheSeedPeersGiven) {
  Start(3, ServeBody);
  auto scheduler_client =
      SchedulerClient::Create(scheduler_->endpoint(), std::chrono::seconds(5));
  ASSERT_TRUE(scheduler_client.ok()) << scheduler_client.status();
  auto seed_peers = (*scheduler_client)->ListSeedPeers();
  ASSERT_TRUE(seed_peers.ok()) << seed_peers.status();
  ASSERT_THAT(*seed_peers, SizeIs(3));

  auto proxy = ProxyWithSeedPeers::Create(*seed_peers);
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  GetRequest request = TestGetRequest();
  auto response = (*proxy)->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(ReadAll(response->body.get()), kBody);

  std::ostringstream output;
  ASSERT_TRUE((*proxy)->GetInto(request, output).ok());
  EXPECT_EQ(output.str(), kBody);
  EXPECT_THAT(*(*proxy)->LookupEndpoints(request), SizeIs(2));

  (*proxy)->UpdateSeedPeers({(*seed_peers)[0]});
  EXPECT_THAT(*(*proxy)->LookupEndpoints(request),
              ElementsAre(proxies_[0]->endpoint()));

  (*proxy)->UpdateSeedPeers({});
  EXPECT_THAT(*(*proxy)->LookupEndpoints(request),
              ElementsAre(proxies_[0]->endpoint()));

  (*seed_peers)[0].healthy = false;
  (*proxy)->UpdateSeedPeers({(*seed_peers)[0]});
  EXPECT_EQ((*proxy)->Get(request).status().code(),
            absl::StatusCode::kInternal);
}

}  // namespace
}  // namespace dragonfly::client_request
