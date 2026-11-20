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

#include "src/get.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <algorithm>
#include <chrono>
#include <cstring>
#include <memory>
#include <sstream>
#include <string>
#include <utility>
#include <vector>

#include "dragonfly/client_request/errors.h"
#include "src/errors_internal.h"
#include "src/testing/http_server.h"

namespace dragonfly::client_request::internal {
namespace {

using ::testing::ElementsAre;
using ::testing::ElementsAreArray;
using ::testing::HasSubstr;
using testing::HeaderValue;
using testing::HttpServer;
using ::testing::IsEmpty;
using testing::TestRequest;
using testing::TestResponse;

constexpr char kUrl[] = "http://example.com/file.txt";

struct Answer {
  int status_code = 206;
  std::string content_range;
  std::int64_t content_length = -1;
  std::string body;
  absl::Status body_error;
  absl::Status error;
};

Answer Partial(std::string content_range, std::string body,
               absl::Status body_error = absl::OkStatus()) {
  Answer answer;
  answer.content_range = std::move(content_range);
  answer.body = std::move(body);
  answer.body_error = std::move(body_error);
  return answer;
}

Answer Whole(std::int64_t content_length, std::string body,
             absl::Status body_error = absl::OkStatus()) {
  Answer answer;
  answer.status_code = 200;
  answer.content_length = content_length;
  answer.body = std::move(body);
  answer.body_error = std::move(body_error);
  return answer;
}

Answer Failure(absl::Status error) {
  Answer answer;
  answer.error = std::move(error);
  return answer;
}

absl::Status BrokenOff() { return absl::InternalError("unexpected EOF"); }

class FakeResponse : public HttpResponse {
 public:
  FakeResponse(Answer answer, int* open_responses)
      : answer_(std::move(answer)), open_responses_(open_responses) {
    if (!answer_.content_range.empty()) {
      header_.emplace("content-range", answer_.content_range);
    }

    ++*open_responses_;
  }

  ~FakeResponse() override { --*open_responses_; }

  int status_code() const override { return answer_.status_code; }

  const Header& header() const override { return header_; }

  std::int64_t content_length() const override {
    return answer_.content_length;
  }

  absl::StatusOr<std::size_t> Read(char* buffer, std::size_t size) override {
    if (offset_ == answer_.body.size()) {
      if (!answer_.body_error.ok()) {
        return answer_.body_error;
      }

      return 0;
    }

    const std::size_t n = std::min(size, answer_.body.size() - offset_);
    std::memcpy(buffer, answer_.body.data() + offset_, n);
    offset_ += n;
    return n;
  }

 private:
  const Answer answer_;
  int* const open_responses_;
  Header header_;
  std::size_t offset_ = 0;
};

class FakeSender : public Sender {
 public:
  explicit FakeSender(std::vector<Answer> answers)
      : answers_(std::move(answers)) {}

  absl::StatusOr<std::unique_ptr<HttpResponse>> SendTo(
      const std::string& endpoint, const HttpRequest& request) override {
    endpoints.push_back(endpoint);
    if (request.header.count("Range") > 0) {
      ranges.push_back(HeaderValue(request.header, "Range"));
    }

    Answer answer = answers_.at(endpoints.size() - 1);
    if (!answer.error.ok()) {
      return answer.error;
    }

    return std::unique_ptr<HttpResponse>(
        std::make_unique<FakeResponse>(std::move(answer), &open_responses));
  }

  std::vector<std::string> endpoints;
  std::vector<std::string> ranges;
  int open_responses = 0;

 private:
  const std::vector<Answer> answers_;
};

GetRequest TestGetRequest() {
  GetRequest request;
  request.url = kUrl;
  return request;
}

absl::Status ReadAll(Body* body, std::string* content) {
  char buffer[4];
  for (;;) {
    auto n = body->Read(buffer, sizeof(buffer));
    if (!n.ok()) {
      return n.status();
    }

    if (*n == 0) {
      return absl::OkStatus();
    }

    content->append(buffer, *n);
  }
}

TEST(AttemptsTest, CreateFailsWithoutEndpoints) {
  auto attempts = Attempts::Create(RetryPolicy(), {});
  EXPECT_EQ(attempts.status().code(), absl::StatusCode::kInvalidArgument);
  EXPECT_EQ(attempts.status().message(), "no endpoints to send request");
}

TEST(AttemptsTest, RunAimsEveryAttemptAtTheNextEndpoint) {
  struct TestCase {
    std::uint8_t max_retries;
    std::vector<absl::Status> answers;
    absl::StatusCode expected_code;
    std::size_t expected_attempts;
  };

  const TestCase test_cases[] = {
      {3,
       {absl::UnavailableError("down"), absl::DeadlineExceededError("slow"),
        absl::NotFoundError("gone")},
       absl::StatusCode::kNotFound,
       3},
      {1,
       {absl::InternalError("boom"), absl::InternalError("boom"),
        absl::InternalError("boom")},
       absl::StatusCode::kInternal,
       2},
      {0, {absl::InternalError("boom")}, absl::StatusCode::kInternal, 1},
      {3,
       {absl::InvalidArgumentError("bad")},
       absl::StatusCode::kInvalidArgument,
       1},
  };

  for (const auto& test_case : test_cases) {
    auto attempts =
        Attempts::Create(RetryPolicy(test_case.max_retries), {"a", "b"});
    ASSERT_TRUE(attempts.ok());

    std::vector<std::string> endpoints;
    auto response =
        attempts->Run([&](const std::string& endpoint)
                          -> absl::StatusOr<std::unique_ptr<HttpResponse>> {
          endpoints.push_back(endpoint);
          return test_case.answers[endpoints.size() - 1];
        });
    EXPECT_EQ(response.status().code(), test_case.expected_code);
    ASSERT_EQ(endpoints.size(), test_case.expected_attempts);
    for (std::size_t i = 2; i < endpoints.size(); ++i) {
      EXPECT_EQ(endpoints[i], endpoints[i - 2]);
    }

    if (endpoints.size() > 1) {
      EXPECT_NE(endpoints[0], endpoints[1]);
    }
  }
}

TEST(AttemptsTest, NextDelayKeepsTheRetriesLeftAcrossRuns) {
  auto attempts = Attempts::Create(RetryPolicy(2), {"a"});
  ASSERT_TRUE(attempts.ok());

  int calls = 0;
  auto send =
      [&calls](
          const std::string&) -> absl::StatusOr<std::unique_ptr<HttpResponse>> {
    ++calls;
    return absl::InternalError("boom");
  };
  EXPECT_FALSE(attempts->Run(send).ok());
  EXPECT_EQ(calls, 3);
  EXPECT_FALSE(attempts->NextDelay().has_value());

  calls = 0;
  EXPECT_FALSE(attempts->Run(send).ok());
  EXPECT_EQ(calls, 1);
}

TEST(NewProxyRequestTest, SendsTheCanonicalUrlWithTheP2pHeaders) {
  GetRequest request;
  request.url = "HTTP://Example.com:80/a b/../file.txt?k=a b";
  request.header = {{"X-Custom", "value"}};
  request.tag = "tag";
  request.timeout = std::chrono::seconds(7);
  auto http_request = NewProxyRequest(request, std::chrono::seconds(5));
  ASSERT_TRUE(http_request.ok()) << http_request.status();
  EXPECT_EQ(http_request->url, "http://example.com/file.txt?k=a%20b");
  EXPECT_EQ(HeaderValue(http_request->header, "X-Custom"), "value");
  EXPECT_EQ(HeaderValue(http_request->header, "X-Dragonfly-Tag"), "tag");
  EXPECT_EQ(HeaderValue(http_request->header, "X-Dragonfly-Use-P2P"), "true");
  EXPECT_EQ(http_request->timeout, std::chrono::seconds(7));
  EXPECT_EQ(http_request->read_timeout, std::chrono::seconds(5));
  EXPECT_TRUE(http_request->insecure_skip_verify);
  EXPECT_TRUE(http_request->proxy.empty());

  request.header = {{"Invalid Name", "value"}};
  EXPECT_EQ(NewProxyRequest(request, std::chrono::seconds(5)).status().code(),
            absl::StatusCode::kInvalidArgument);
}

TEST(SendGetTest, SendsTheRequestThroughTheProxy) {
  HttpServer proxy([](const TestRequest&) {
    TestResponse response;
    response.body = "hello dragonfly";
    return response;
  });

  GetRequest request = TestGetRequest();
  request.tag = "tag";
  auto http_request = NewProxyRequest(request, std::chrono::seconds(5));
  ASSERT_TRUE(http_request.ok()) << http_request.status();
  auto client = HttpClient::Create();
  auto response = SendGet(*client, proxy.endpoint(), *http_request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ((*response)->status_code(), 200);

  ASSERT_EQ(proxy.requests(), 1);
  const TestRequest received = proxy.received()[0];
  EXPECT_EQ(received.target, kUrl);
  EXPECT_EQ(HeaderValue(received.header, "x-dragonfly-tag"), "tag");
  EXPECT_EQ(HeaderValue(received.header, "x-dragonfly-use-p2p"), "true");
}

TEST(SendGetTest, ConvertsAnAnswerIntoAnErrorByItsType) {
  struct TestCase {
    std::string error_type;
    int status_code;
    ErrorType expected_type;
    std::string expected_message;
    bool expected_retryable;
  };

  const TestCase test_cases[] = {
      {"backend", 404, ErrorType::kBackend, "failed", false},
      {"backend", 500, ErrorType::kBackend, "failed", true},
      {"proxy", 502, ErrorType::kProxy, "failed", true},
      {"proxy", 401, ErrorType::kProxy, "failed", false},
      {"dfdaemon", 507, ErrorType::kDfdaemon, "failed", true},
      {"dfdaemon", 422, ErrorType::kDfdaemon, "failed", false},
      {"", 503, ErrorType::kProxy, "unexpected status code from proxy: 503",
       true},
      {"other", 400, ErrorType::kProxy, "unknown error type from proxy: other",
       false},
  };

  for (const auto& test_case : test_cases) {
    HttpServer proxy([&test_case](const TestRequest&) {
      TestResponse response;
      response.status_code = test_case.status_code;
      response.body = "failed";
      if (!test_case.error_type.empty()) {
        response.header = {{"X-Dragonfly-Error-Type", test_case.error_type}};
      }

      return response;
    });

    auto http_request =
        NewProxyRequest(TestGetRequest(), std::chrono::seconds(5));
    ASSERT_TRUE(http_request.ok()) << http_request.status();
    auto client = HttpClient::Create();
    auto response = SendGet(*client, proxy.endpoint(), *http_request);
    ASSERT_FALSE(response.ok());

    auto detail = GetErrorDetail(response.status());
    ASSERT_TRUE(detail.has_value()) << response.status();
    EXPECT_EQ(detail->type, test_case.expected_type);
    EXPECT_EQ(detail->status_code, test_case.status_code);
    EXPECT_EQ(detail->message, test_case.expected_message);
    EXPECT_EQ(IsRetryable(response.status()), test_case.expected_retryable);
  }
}

TEST(ProxyGetterTest, GetSendsTheBodyAgainForTheBytesLeftWhenItBreaksOff) {
  struct TestCase {
    std::string name;
    std::uint8_t max_retries;
    std::vector<Answer> answers;
    absl::StatusCode expected_code;
    std::string expected_message;
    std::string expected_content;
    std::vector<std::string> expected_ranges;
  };

  const TestCase test_cases[] = {
      {"reads a body that does not break off",
       1,
       {Partial("bytes 0-14/15", "hello dragonfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {}},
      {"resumes a body that breaks off",
       1,
       {Partial("bytes 0-14/15", "hello ", BrokenOff()),
        Partial("bytes 6-14/15", "dragonfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {"bytes=6-14"}},
      {"resumes a whole body by its content length",
       1,
       {Whole(15, "hello ", BrokenOff()),
        Partial("bytes 6-14/15", "dragonfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {"bytes=6-14"}},
      {"resumes a body that ends short",
       1,
       {Partial("bytes 0-14/15", "hello "),
        Partial("bytes 6-14/15", "dragonfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {"bytes=6-14"}},
      {"resumes a body that times out",
       1,
       {Partial("bytes 0-14/15", "hello ", absl::DeadlineExceededError("slow")),
        Partial("bytes 6-14/15", "dragonfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {"bytes=6-14"}},
      {"resumes inside the range of the request",
       1,
       {Partial("bytes 100-114/1000", "hello ", BrokenOff()),
        Partial("bytes 106-114/1000", "dragonfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {"bytes=106-114"}},
      {"resumes a body that breaks off twice",
       2,
       {Partial("bytes 0-14/15", "hello ", BrokenOff()),
        Partial("bytes 6-14/15", "drag", BrokenOff()),
        Partial("bytes 10-14/15", "onfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {"bytes=6-14", "bytes=10-14"}},
      {"sends the request again after a response of other bytes",
       2,
       {Partial("bytes 0-14/15", "hello ", BrokenOff()),
        Partial("bytes 0-14/15", "hello dragonfly"),
        Partial("bytes 6-14/15", "dragonfly")},
       absl::StatusCode::kOk,
       "",
       "hello dragonfly",
       {"bytes=6-14", "bytes=6-14"}},
      {"fails without a retry left",
       0,
       {Partial("bytes 0-14/15", "hello ", BrokenOff())},
       absl::StatusCode::kInternal,
       "failed to read response body: unexpected EOF",
       "hello ",
       {}},
      {"fails when it ends short without a retry left",
       0,
       {Partial("bytes 0-14/15", "hello ")},
       absl::StatusCode::kInternal,
       "response body ended without bytes=6-14",
       "hello ",
       {}},
      {"fails with a timeout without a retry left",
       0,
       {Partial("bytes 0-14/15", "hello ",
                absl::DeadlineExceededError("slow"))},
       absl::StatusCode::kDeadlineExceeded,
       "slow",
       "hello ",
       {}},
      {"fails without a known length",
       1,
       {Whole(-1, "hello ", BrokenOff())},
       absl::StatusCode::kInternal,
       "failed to read response body: unexpected EOF",
       "hello ",
       {}},
      {"fails on a response of another range",
       1,
       {Partial("bytes 0-14/15", "hello ", BrokenOff()),
        Partial("bytes 0-14/15", "hello dragonfly")},
       absl::StatusCode::kInternal,
       "response sent again does not resume the body",
       "hello ",
       {"bytes=6-14"}},
      {"fails on a response of another complete length",
       1,
       {Partial("bytes 0-14/15", "hello ", BrokenOff()),
        Partial("bytes 6-14/16", "dragonfly!")},
       absl::StatusCode::kInternal,
       "response sent again does not resume the body",
       "hello ",
       {"bytes=6-14"}},
      {"fails on a definitive answer",
       1,
       {Partial("bytes 0-14/15", "hello ", BrokenOff()),
        Failure(absl::NotFoundError("gone"))},
       absl::StatusCode::kNotFound,
       "gone",
       "hello ",
       {"bytes=6-14"}},
  };

  for (const auto& test_case : test_cases) {
    SCOPED_TRACE(test_case.name);
    auto sender = std::make_shared<FakeSender>(test_case.answers);
    const ProxyGetter getter(sender, RetryPolicy(test_case.max_retries),
                             std::chrono::seconds(1));
    auto response = getter.Get(TestGetRequest(), {"a", "b"});
    ASSERT_TRUE(response.ok()) << response.status();
    EXPECT_TRUE(response->success);
    EXPECT_EQ(response->status_code, test_case.answers[0].status_code);

    std::string content;
    const absl::Status status = ReadAll(response->body.get(), &content);
    EXPECT_EQ(status.code(), test_case.expected_code) << status;
    EXPECT_THAT(std::string(status.message()),
                HasSubstr(test_case.expected_message));
    EXPECT_EQ(content, test_case.expected_content);
    EXPECT_THAT(sender->ranges, ElementsAreArray(test_case.expected_ranges));
    EXPECT_EQ(sender->endpoints.size(), test_case.answers.size());
    EXPECT_EQ(sender->open_responses, 0);

    char buffer[4];
    EXPECT_EQ(response->body->Read(buffer, sizeof(buffer)).status().code(),
              test_case.expected_code);
  }
}

TEST(ProxyGetterTest, GetRetriesTheNextEndpointBeforeItAnswers) {
  auto sender = std::make_shared<FakeSender>(std::vector<Answer>{
      Failure(absl::UnavailableError("down")), Whole(15, "hello dragonfly")});
  const ProxyGetter getter(sender, RetryPolicy(1), std::chrono::seconds(1));
  auto response = getter.Get(TestGetRequest(), {"a", "b"});
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(response->status_code, 200);
  ASSERT_EQ(sender->endpoints.size(), 2U);
  EXPECT_NE(sender->endpoints[0], sender->endpoints[1]);

  sender = std::make_shared<FakeSender>(
      std::vector<Answer>{Failure(absl::NotFoundError("gone"))});
  EXPECT_EQ(ProxyGetter(sender, RetryPolicy(3), std::chrono::seconds(1))
                .Get(TestGetRequest(), {"a", "b"})
                .status()
                .code(),
            absl::StatusCode::kNotFound);
  EXPECT_EQ(sender->endpoints.size(), 1U);
}

TEST(ProxyGetterTest, AReadOfNoBytesLeavesTheBodyAsItIs) {
  auto sender = std::make_shared<FakeSender>(
      std::vector<Answer>{Whole(15, "hello dragonfly")});
  const ProxyGetter getter(sender, RetryPolicy(3), std::chrono::seconds(1));
  auto response = getter.Get(TestGetRequest(), {"a"});
  ASSERT_TRUE(response.ok()) << response.status();

  char buffer[4];
  auto n = response->body->Read(buffer, sizeof(buffer));
  ASSERT_TRUE(n.ok()) << n.status();
  EXPECT_EQ(*n, 4U);
  n = response->body->Read(buffer, 0);
  ASSERT_TRUE(n.ok()) << n.status();
  EXPECT_EQ(*n, 0U);

  std::string content;
  EXPECT_TRUE(ReadAll(response->body.get(), &content).ok());
  EXPECT_EQ(content, "o dragonfly");
  EXPECT_THAT(sender->endpoints, ElementsAre("a"));
}

TEST(ProxyGetterTest, GetIntoWritesTheBodyIntoTheOutput) {
  auto sender = std::make_shared<FakeSender>(
      std::vector<Answer>{Partial("bytes 0-14/15", "hello ", BrokenOff()),
                          Partial("bytes 6-14/15", "dragonfly")});
  const ProxyGetter getter(sender, RetryPolicy(1), std::chrono::seconds(1));
  std::ostringstream output;
  auto response = getter.GetInto(TestGetRequest(), {"a", "b"}, output);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_EQ(response->status_code, 206);
  EXPECT_EQ(response->body, nullptr);
  EXPECT_EQ(output.str(), "hello dragonfly");
  EXPECT_THAT(sender->ranges, ElementsAre("bytes=6-14"));

  sender = std::make_shared<FakeSender>(
      std::vector<Answer>{Whole(15, "hello ", BrokenOff())});
  std::ostringstream partial;
  response = ProxyGetter(sender, RetryPolicy(0), std::chrono::seconds(1))
                 .GetInto(TestGetRequest(), {"a"}, partial);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kInternal);
  EXPECT_EQ(partial.str(), "hello ");

  sender = std::make_shared<FakeSender>(
      std::vector<Answer>{Whole(15, "hello dragonfly")});
  std::ostringstream closed;
  closed.setstate(std::ios::badbit);
  response = ProxyGetter(sender, RetryPolicy(0), std::chrono::seconds(1))
                 .GetInto(TestGetRequest(), {"a"}, closed);
  EXPECT_EQ(response.status().code(), absl::StatusCode::kInternal);
  EXPECT_EQ(response.status().message(), "failed to write response body");
  EXPECT_THAT(sender->ranges, IsEmpty());
}

}  // namespace
}  // namespace dragonfly::client_request::internal
