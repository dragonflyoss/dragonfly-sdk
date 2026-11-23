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

#include "src/http_client.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <chrono>
#include <memory>
#include <string>

#include "src/testing/http_server.h"

namespace dragonfly::client_request::internal {
namespace {

using std::chrono::milliseconds;
using std::chrono::steady_clock;
using testing::HeaderValue;
using testing::HttpServer;
using testing::TestRequest;
using testing::TestResponse;

absl::StatusOr<std::string> ReadAll(HttpResponse* response,
                                    std::size_t buffer_size = 4096) {
  std::string body;
  std::string buffer(buffer_size, '\0');
  for (;;) {
    auto n = response->Read(buffer.data(), buffer.size());
    if (!n.ok()) {
      return n.status();
    }

    if (*n == 0) {
      return body;
    }

    body.append(buffer, 0, *n);
  }
}

std::string LargeBody(std::size_t size) {
  std::string body(size, '\0');
  for (std::size_t i = 0; i < size; ++i) {
    body[i] = static_cast<char>('a' + ((i * 31 + i / 251) % 26));
  }

  return body;
}

TEST(HttpClientTest, GetReturnsTheResponse) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.header = {{"Content-Type", "text/plain"}, {"X-Custom", "value"}};
    response.body = "hello dragonfly";
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint() + "/file.txt?a=b";
  request.header = {{"X-Request", "1"}};
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ((*response)->status_code(), 200);
  EXPECT_EQ((*response)->content_length(), 15);
  EXPECT_EQ(HeaderValue((*response)->header(), "content-type"), "text/plain");
  EXPECT_EQ(HeaderValue((*response)->header(), "x-custom"), "value");
  EXPECT_EQ(*ReadAll(response->get()), "hello dragonfly");

  ASSERT_EQ(server.requests(), 1);
  const TestRequest received = server.received()[0];
  EXPECT_EQ(received.method, "GET");
  EXPECT_EQ(received.target, "/file.txt?a=b");
  EXPECT_EQ(HeaderValue(received.header, "x-request"), "1");
}

TEST(HttpClientTest, GetSendsTheRequestThroughTheProxy) {
  HttpServer proxy([](const TestRequest&) {
    TestResponse response;
    response.body = "proxied";
    return response;
  });

  HttpRequest request;
  request.url = "http://example.com/file.txt";
  request.proxy = proxy.endpoint();
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(*ReadAll(response->get()), "proxied");

  ASSERT_EQ(proxy.requests(), 1);
  EXPECT_EQ(proxy.received()[0].target, "http://example.com/file.txt");
  EXPECT_EQ(HeaderValue(proxy.received()[0].header, "host"), "example.com");
}

TEST(HttpClientTest, GetSendsAHeaderWithoutAValue) {
  HttpServer server([](const TestRequest&) { return TestResponse(); });

  HttpRequest request;
  request.url = server.endpoint() + "/file.txt";
  request.header = {{"X-Empty", ""}, {"X-Request", "1"}};
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();

  ASSERT_EQ(server.requests(), 1);
  const TestRequest received = server.received()[0];
  EXPECT_EQ(received.header.count("x-empty"), 1U);
  EXPECT_EQ(HeaderValue(received.header, "x-empty"), "");
  EXPECT_EQ(HeaderValue(received.header, "x-request"), "1");
}

TEST(HttpClientTest, GetFollowsTheRedirects) {
  HttpServer server([](const TestRequest& request) {
    TestResponse response;
    if (request.target == "/file.txt") {
      response.status_code = 302;
      response.header = {{"Location", "/moved.txt"}};
      response.body = "moved";
    } else {
      response.header = {{"X-Custom", "value"}};
      response.body = "hello dragonfly";
    }

    return response;
  });

  HttpRequest request;
  request.url = server.endpoint() + "/file.txt";
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ((*response)->status_code(), 200);
  EXPECT_EQ((*response)->header().count("location"), 0U);
  EXPECT_EQ(HeaderValue((*response)->header(), "x-custom"), "value");
  EXPECT_EQ(*ReadAll(response->get()), "hello dragonfly");
  ASSERT_EQ(server.requests(), 2);
  EXPECT_EQ(server.received()[1].target, "/moved.txt");
}

TEST(HttpClientTest, GetFailsAfterTooManyRedirects) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.status_code = 302;
    response.header = {{"Location", "/file.txt"}};
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint() + "/file.txt";
  auto response = HttpClient::Create()->Get(request);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kInternal);
  EXPECT_FALSE(IsConnectError(response.status()));
  EXPECT_EQ(server.requests(), 11);
}

TEST(HttpClientTest, GetReturnsARedirectWithoutALocation) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.status_code = 304;
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint() + "/file.txt";
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ((*response)->status_code(), 304);
}

TEST(HttpClientTest, GetBearsTheLongestTimeouts) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = "hello dragonfly";
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint() + "/file.txt";
  request.timeout = milliseconds::max();
  request.read_timeout = milliseconds::max();
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(*ReadAll(response->get()), "hello dragonfly");
}

TEST(HttpClientTest, GetReturnsAnAnswerWhateverItsStatusCode) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.status_code = 404;
    response.body = "not found";
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint();
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ((*response)->status_code(), 404);
  EXPECT_EQ(*ReadAll(response->get()), "not found");
}

TEST(HttpClientTest, ReadStreamsTheBodyInPieces) {
  const std::string body = LargeBody((3 * 1024 * 1024) + 17);
  HttpServer server([&body](const TestRequest&) {
    TestResponse response;
    response.body = body;
    return response;
  });

  auto client = HttpClient::Create();
  for (std::size_t buffer_size : {1U, 1000U, 64U * 1024, 8U * 1024 * 1024}) {
    HttpRequest request;
    request.url = server.endpoint();
    auto response = client->Get(request);
    ASSERT_TRUE(response.ok()) << response.status();
    if (buffer_size == 1) {
      char byte = 0;
      auto n = (*response)->Read(&byte, 1);
      ASSERT_TRUE(n.ok()) << n.status();
      EXPECT_EQ(*n, 1U);
      EXPECT_EQ(byte, body[0]);
      continue;
    }

    auto read = ReadAll(response->get(), buffer_size);
    ASSERT_TRUE(read.ok()) << read.status();
    EXPECT_TRUE(*read == body) << "buffer size: " << buffer_size;
  }
}

TEST(HttpClientTest, ReadsABodyWithoutALength) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = "hello dragonfly";
    response.no_content_length = true;
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint();
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ((*response)->content_length(), -1);
  EXPECT_EQ(*ReadAll(response->get()), "hello dragonfly");
}

TEST(HttpClientTest, GetFailsWhenTheHeadersStallPastTheReadTimeout) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.header_delay = milliseconds(3000);
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint();
  request.read_timeout = milliseconds(100);
  const auto start = steady_clock::now();
  auto response = HttpClient::Create()->Get(request);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kDeadlineExceeded);
  EXPECT_LT(steady_clock::now() - start, milliseconds(2000));
}

TEST(HttpClientTest, ReadFailsWhenTheBodyStallsPastTheReadTimeout) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = "hello dragonfly";
    response.stall_after = 5;
    response.stall = milliseconds(3000);
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint();
  request.read_timeout = milliseconds(500);
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();

  std::string head(5, '\0');
  std::size_t read = 0;
  while (read < head.size()) {
    auto n = (*response)->Read(&head[read], head.size() - read);
    ASSERT_TRUE(n.ok()) << n.status();
    ASSERT_GT(*n, 0U);
    read += *n;
  }
  EXPECT_EQ(head, "hello");

  const auto start = steady_clock::now();
  char buffer[16];
  auto n = (*response)->Read(buffer, sizeof(buffer));
  EXPECT_EQ(n.status().code(), absl::StatusCode::kDeadlineExceeded);
  EXPECT_LT(steady_clock::now() - start, milliseconds(2000));
}

TEST(HttpClientTest, GetFailsWhenTheRequestOutlastsItsTimeout) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.header_delay = milliseconds(3000);
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint();
  request.timeout = milliseconds(100);
  const auto start = steady_clock::now();
  auto response = HttpClient::Create()->Get(request);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kDeadlineExceeded);
  EXPECT_LT(steady_clock::now() - start, milliseconds(2000));
}

TEST(HttpClientTest, ReadFailsWhenTheBodyBreaksOff) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = "hello dragonfly";
    response.send_body_bytes = 6;
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint();
  auto response = HttpClient::Create()->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ((*response)->content_length(), 15);

  std::string content;
  char buffer[4];
  absl::StatusOr<std::size_t> n = 0;
  while ((n = (*response)->Read(buffer, sizeof(buffer))).ok()) {
    ASSERT_GT(*n, 0U);
    content.append(buffer, *n);
  }

  EXPECT_EQ(content, "hello ");
  EXPECT_EQ(n.status().code(), absl::StatusCode::kInternal);
}

TEST(HttpClientTest, GetFailsWhenTheServerAnswersNothing) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.refuse = true;
    return response;
  });

  HttpRequest request;
  request.url = server.endpoint();
  auto response = HttpClient::Create()->Get(request);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kInternal);
  EXPECT_FALSE(IsConnectError(response.status()));
}

TEST(HttpClientTest, GetMarksAFailureToConnect) {
  HttpRequest request;
  request.url = "http://example.com/file.txt";
  request.proxy = "http://127.0.0.1:1";
  auto response = HttpClient::Create()->Get(request);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kInternal);
  EXPECT_TRUE(IsConnectError(response.status())) << response.status();
  EXPECT_FALSE(IsConnectError(absl::InternalError("boom")));
}

TEST(HttpClientTest, GetReusesTheConnection) {
  HttpServer server([](const TestRequest&) {
    TestResponse response;
    response.body = "hello dragonfly";
    return response;
  });

  auto client = HttpClient::Create();
  for (int i = 0; i < 3; ++i) {
    HttpRequest request;
    request.url = server.endpoint();
    auto response = client->Get(request);
    ASSERT_TRUE(response.ok()) << response.status();
    EXPECT_EQ(*ReadAll(response->get()), "hello dragonfly");
  }

  EXPECT_EQ(server.requests(), 3);
  EXPECT_EQ(server.connections(), 1);
}

TEST(HttpClientTest, AnAbandonedResponseLeavesTheClientUsable) {
  const std::string body = LargeBody(4 * 1024 * 1024);
  HttpServer server([&body](const TestRequest&) {
    TestResponse response;
    response.body = body;
    return response;
  });

  auto client = HttpClient::Create();
  HttpRequest request;
  request.url = server.endpoint();
  {
    auto response = client->Get(request);
    ASSERT_TRUE(response.ok()) << response.status();
    char buffer[100];
    ASSERT_TRUE((*response)->Read(buffer, sizeof(buffer)).ok());
  }

  auto response = client->Get(request);
  ASSERT_TRUE(response.ok()) << response.status();
  auto read = ReadAll(response->get(), 64 * 1024);
  ASSERT_TRUE(read.ok()) << read.status();
  EXPECT_TRUE(*read == body);

  std::unique_ptr<HttpResponse> outliving = std::move(*client->Get(request));
  client.reset();
  EXPECT_TRUE(*ReadAll(outliving.get(), 64 * 1024) == body);
}

}  // namespace
}  // namespace dragonfly::client_request::internal
