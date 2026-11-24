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

#include "dragonfly/client_request/proxy_with_endpoints.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <atomic>
#include <chrono>
#include <sstream>
#include <string>
#include <thread>
#include <vector>

#include "dragonfly/client_request/errors.h"
#include "src/testing/http_server.h"

namespace dragonfly::client_request {
namespace {

using internal::testing::HeaderValue;
using internal::testing::HttpServer;
using internal::testing::TestRequest;
using internal::testing::TestResponse;
using std::chrono::milliseconds;
using std::chrono::steady_clock;
using ::testing::HasSubstr;

constexpr char kUrl[] = "http://example.com/file.txt";
constexpr char kBody[] = "hello dragonfly";

GetRequest TestGetRequest() {
  GetRequest request;
  request.url = kUrl;
  request.timeout = std::chrono::seconds(10);
  return request;
}

absl::StatusOr<std::string> ReadAll(Body* body) {
  std::string content;
  char buffer[4];
  for (;;) {
    auto n = body->Read(buffer, sizeof(buffer));
    if (!n.ok()) {
      return n.status();
    }

    if (*n == 0) {
      return content;
    }

    content.append(buffer, *n);
  }
}

TestResponse ServeRange(const TestRequest& request) {
  TestResponse response;
  const auto range = request.header.find("range");
  if (range == request.header.end()) {
    response.body = kBody;
    return response;
  }

  const std::size_t dash = range->second.find('-');
  const std::size_t start = std::stoul(range->second.substr(6, dash - 6));
  const std::size_t end = std::stoul(range->second.substr(dash + 1));
  response.status_code = 206;
  response.header = {{"Content-Range", "bytes " + std::to_string(start) + "-" +
                                           std::to_string(end) + "/15"}};
  response.body = std::string(kBody).substr(start, end - start + 1);
  return response;
}

TEST(ProxyWithEndpointsTest, CreateValidatesTheEndpointsAndTheOptions) {
  auto proxy = ProxyWithEndpoints::Create({});
  EXPECT_EQ(proxy.status().code(), absl::StatusCode::kInvalidArgument);
  EXPECT_EQ(proxy.status().message(), "endpoints must not be empty");

  ProxyWithEndpointsOptions options;
  options.read_timeout = milliseconds(0);
  proxy = ProxyWithEndpoints::Create({"http://127.0.0.1:4001"}, options);
  EXPECT_EQ(proxy.status().message(), "read timeout must be greater than 0");

  options = ProxyWithEndpointsOptions();
  options.max_retries = 4;
  proxy = ProxyWithEndpoints::Create({"http://127.0.0.1:4001"}, options);
  EXPECT_EQ(proxy.status().message(), "max retries must be between 0 and 3");

  for (const char* endpoint :
       {"", "://", "127.0.0.1:4001", "http://", "ftp://127.0.0.1:4001"}) {
    proxy = ProxyWithEndpoints::Create({"http://127.0.0.1:4001", endpoint});
    EXPECT_EQ(proxy.status().code(), absl::StatusCode::kInvalidArgument)
        << endpoint;
    EXPECT_THAT(std::string(proxy.status().message()),
                HasSubstr("must be http or https with a host"));
  }

  EXPECT_TRUE(ProxyWithEndpoints::Create({"http://127.0.0.1:4001"}).ok());
}

TEST(ProxyWithEndpointsTest, GetReturnsTheResponseWithAStreamingBody) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.header = {{"Content-Type", "text/plain"}};
    response.body = kBody;
    return response;
  });

  auto proxy = ProxyWithEndpoints::Create({server.endpoint()});
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  auto response = (*proxy)->Get(TestGetRequest());
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_TRUE(response->success);
  EXPECT_EQ(response->status_code, 200);
  EXPECT_EQ(HeaderValue(response->header, "content-type"), "text/plain");
  EXPECT_EQ(*ReadAll(response->body.get()), kBody);
  EXPECT_EQ(*response->body->Read(nullptr, 0), 0U);
  EXPECT_EQ(server.received()[0].target, kUrl);
}

TEST(ProxyWithEndpointsTest, GetRejectsAnInvalidRequest) {
  HttpServer server([](const TestRequest&) { return TestResponse(); });
  auto proxy = ProxyWithEndpoints::Create({server.endpoint()});
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  GetRequest request = TestGetRequest();
  request.url = "ftp://example.com/file.txt";
  EXPECT_EQ((*proxy)->Get(request).status().code(),
            absl::StatusCode::kInvalidArgument);

  request = TestGetRequest();
  request.replicas = 0;
  std::ostringstream output;
  EXPECT_EQ((*proxy)->GetInto(request, output).status().code(),
            absl::StatusCode::kInvalidArgument);
  EXPECT_EQ(server.requests(), 0);
}

TEST(ProxyWithEndpointsTest, GetRetriesOnlyATransientAnswer) {
  struct TestCase {
    int status_code;
    bool expected_retry;
  };

  const TestCase test_cases[] = {
      {500, true}, {502, true},  {503, true},  {408, true},
      {429, true}, {404, false}, {403, false}, {416, false},
  };

  for (const auto& test_case : test_cases) {
    std::atomic<int> requests{0};
    auto handler = [&](const TestRequest&) {
      TestResponse response;
      if (++requests == 1) {
        response.status_code = test_case.status_code;
        response.header = {{"X-Dragonfly-Error-Type", "backend"}};
        response.body = "failed";
      } else {
        response.body = kBody;
      }

      return response;
    };
    HttpServer first(handler);
    HttpServer second(handler);

    auto proxy =
        ProxyWithEndpoints::Create({first.endpoint(), second.endpoint()});
    ASSERT_TRUE(proxy.ok()) << proxy.status();
    auto response = (*proxy)->Get(TestGetRequest());
    if (test_case.expected_retry) {
      ASSERT_TRUE(response.ok()) << response.status();
      EXPECT_EQ(*ReadAll(response->body.get()), kBody);
      EXPECT_EQ(first.requests(), 1);
      EXPECT_EQ(second.requests(), 1);
      continue;
    }

    auto detail = GetErrorDetail(response.status());
    ASSERT_TRUE(detail.has_value()) << response.status();
    EXPECT_EQ(detail->type, ErrorType::kBackend);
    EXPECT_EQ(detail->status_code, test_case.status_code);
    EXPECT_EQ(detail->message, "failed");
    EXPECT_EQ(requests, 1);
  }
}

TEST(ProxyWithEndpointsTest, GetRetriesAfterAFailureToConnect) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = kBody;
    return response;
  });

  auto proxy =
      ProxyWithEndpoints::Create({"http://127.0.0.1:1", server.endpoint()});
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  for (int i = 0; i < 8; ++i) {
    auto response = (*proxy)->Get(TestGetRequest());
    ASSERT_TRUE(response.ok()) << response.status();
    EXPECT_EQ(*ReadAll(response->body.get()), kBody);
  }
}

TEST(ProxyWithEndpointsTest, GetFailsOnceTheRetriesAreExhausted) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.status_code = 503;
    response.header = {{"X-Dragonfly-Error-Type", "proxy"}};
    return response;
  });

  ProxyWithEndpointsOptions options;
  options.max_retries = 2;
  auto proxy = ProxyWithEndpoints::Create({server.endpoint()}, options);
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  auto response = (*proxy)->Get(TestGetRequest());
  EXPECT_EQ(response.status().code(), absl::StatusCode::kUnavailable);
  EXPECT_EQ(server.requests(), 3);

  options.max_retries = 0;
  proxy = ProxyWithEndpoints::Create({server.endpoint()}, options);
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  EXPECT_FALSE((*proxy)->Get(TestGetRequest()).ok());
  EXPECT_EQ(server.requests(), 4);
}

TEST(ProxyWithEndpointsTest, GetRetriesAResponseStalledPastTheReadTimeout) {
  std::atomic<int> requests{0};
  HttpServer server([&requests](const TestRequest&) {
    TestResponse response;
    response.body = kBody;
    if (++requests == 1) {
      response.header_delay = milliseconds(3000);
    }

    return response;
  });

  ProxyWithEndpointsOptions options;
  options.read_timeout = milliseconds(1000);
  auto proxy = ProxyWithEndpoints::Create({server.endpoint()}, options);
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  const auto start = steady_clock::now();
  auto response = (*proxy)->Get(TestGetRequest());
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(*ReadAll(response->body.get()), kBody);
  EXPECT_EQ(requests, 2);
  EXPECT_LT(steady_clock::now() - start, milliseconds(2500));
}

TEST(ProxyWithEndpointsTest, BodyIsSentAgainForTheBytesLeftWhenItBreaksOff) {
  struct TestCase {
    std::string range;
    std::string expected_body;
    std::string expected_resumed_range;
    bool stalls;
  };

  const TestCase test_cases[] = {
      {"", kBody, "bytes=6-14", false},
      {"bytes=2-11", "llo dragon", "bytes=8-11", false},
      {"", kBody, "bytes=6-14", true},
  };

  for (const auto& test_case : test_cases) {
    std::atomic<int> requests{0};
    HttpServer server([&](const TestRequest& request) {
      TestResponse response = ServeRange(request);
      if (++requests == 1) {
        if (test_case.stalls) {
          response.stall_after = 6;
          response.stall = milliseconds(3000);
        } else {
          response.send_body_bytes = 6;
        }
      }

      return response;
    });

    ProxyWithEndpointsOptions options;
    options.read_timeout = milliseconds(1000);
    auto proxy = ProxyWithEndpoints::Create({server.endpoint()}, options);
    ASSERT_TRUE(proxy.ok()) << proxy.status();

    GetRequest request = TestGetRequest();
    if (!test_case.range.empty()) {
      request.header = {{"Range", test_case.range}};
    }

    auto response = (*proxy)->Get(request);
    ASSERT_TRUE(response.ok()) << response.status();
    auto body = ReadAll(response->body.get());
    ASSERT_TRUE(body.ok()) << body.status();
    EXPECT_EQ(*body, test_case.expected_body);
    ASSERT_EQ(server.requests(), 2);
    EXPECT_EQ(HeaderValue(server.received()[1].header, "range"),
              test_case.expected_resumed_range);
    EXPECT_EQ(server.received()[1].header.count("range"), 1U);
  }
}

TEST(ProxyWithEndpointsTest, BodyFailsWhenTheAnswerSentAgainNamesOtherBytes) {
  std::atomic<int> requests{0};
  HttpServer server([&requests](const TestRequest&) {
    TestResponse response;
    response.body = kBody;
    if (++requests == 1) {
      response.send_body_bytes = 6;
    }

    return response;
  });

  auto proxy = ProxyWithEndpoints::Create({server.endpoint()});
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  auto response = (*proxy)->Get(TestGetRequest());
  ASSERT_TRUE(response.ok()) << response.status();

  auto body = ReadAll(response->body.get());
  EXPECT_EQ(body.status().code(), absl::StatusCode::kInternal);
  EXPECT_EQ(body.status().message(),
            "response sent again does not resume the body");
  EXPECT_EQ(server.requests(), 2);

  char buffer[4];
  EXPECT_EQ(response->body->Read(buffer, sizeof(buffer)).status(),
            body.status());
}

TEST(ProxyWithEndpointsTest, BodyFailsWhenItBreaksOffWithoutARetryLeft) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = kBody;
    response.send_body_bytes = 6;
    return response;
  });

  ProxyWithEndpointsOptions options;
  options.max_retries = 0;
  auto proxy = ProxyWithEndpoints::Create({server.endpoint()}, options);
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  auto response = (*proxy)->Get(TestGetRequest());
  ASSERT_TRUE(response.ok()) << response.status();

  std::string content;
  char buffer[4];
  absl::StatusOr<std::size_t> n = 0;
  while ((n = response->body->Read(buffer, sizeof(buffer))).ok()) {
    ASSERT_GT(*n, 0U);
    content.append(buffer, *n);
  }

  EXPECT_EQ(content, "hello ");
  EXPECT_EQ(n.status().code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(n.status().message()),
              HasSubstr("failed to read response body"));
  EXPECT_EQ(server.requests(), 1);
}

TEST(ProxyWithEndpointsTest, BodyWithoutALengthEndsWithTheConnection) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = kBody;
    response.no_content_length = true;
    return response;
  });

  auto proxy = ProxyWithEndpoints::Create({server.endpoint()});
  ASSERT_TRUE(proxy.ok()) << proxy.status();
  auto response = (*proxy)->Get(TestGetRequest());
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(*ReadAll(response->body.get()), kBody);
  EXPECT_EQ(server.requests(), 1);
}

TEST(ProxyWithEndpointsTest, GetServesConcurrentRequests) {
  HttpServer first(ServeRange);
  HttpServer second(ServeRange);
  auto proxy =
      ProxyWithEndpoints::Create({first.endpoint(), second.endpoint()});
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  std::atomic<int> served{0};
  std::vector<std::thread> threads;
  threads.reserve(8);
  for (int i = 0; i < 8; ++i) {
    threads.emplace_back([&] {
      for (int j = 0; j < 20; ++j) {
        auto response = (*proxy)->Get(TestGetRequest());
        if (response.ok() && ReadAll(response->body.get()).ok()) {
          ++served;
        }
      }
    });
  }

  for (auto& thread : threads) {
    thread.join();
  }

  EXPECT_EQ(served, 160);
  EXPECT_EQ(first.requests() + second.requests(), 160);
}

TEST(ProxyWithEndpointsTest, GetIntoWritesTheBodyIntoTheOutput) {
  std::atomic<int> requests{0};
  HttpServer server([&requests](const TestRequest& request) {
    TestResponse response = ServeRange(request);
    if (++requests == 1) {
      response.send_body_bytes = 6;
    }

    return response;
  });

  auto proxy = ProxyWithEndpoints::Create({server.endpoint()});
  ASSERT_TRUE(proxy.ok()) << proxy.status();

  std::ostringstream output;
  auto response = (*proxy)->GetInto(TestGetRequest(), output);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_TRUE(response->success);
  EXPECT_EQ(response->status_code, 200);
  EXPECT_EQ(response->body, nullptr);
  EXPECT_EQ(output.str(), kBody);
  EXPECT_EQ(server.requests(), 2);

  std::ostringstream failed;
  failed.setstate(std::ios::badbit);
  response = (*proxy)->GetInto(TestGetRequest(), failed);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kInternal);
  EXPECT_EQ(response.status().message(), "failed to write response body");
}

}  // namespace
}  // namespace dragonfly::client_request
