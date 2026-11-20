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

#include "dragonfly/client_request/request.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <chrono>
#include <utility>

#include "src/request_internal.h"

namespace dragonfly::client_request::internal {
namespace {

using ::testing::ElementsAre;
using ::testing::HasSubstr;
using ::testing::Pair;

void ExpectInvalidArgument(const absl::Status& status,
                           const std::string& message) {
  EXPECT_EQ(status.code(), absl::StatusCode::kInvalidArgument) << status;
  EXPECT_THAT(std::string(status.message()), HasSubstr(message));
}

TEST(RequestTest, DefaultFilteredQueryParamsHoldsTheSignatureParams) {
  EXPECT_THAT(
      DefaultFilteredQueryParams(),
      ElementsAre("X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date",
                  "X-Amz-Expires", "X-Amz-SignedHeaders", "X-Amz-Signature",
                  "X-Amz-Security-Token", "X-Amz-User-Agent",
                  "X-Goog-Algorithm", "X-Goog-Credential", "X-Goog-Date",
                  "X-Goog-Expires", "X-Goog-SignedHeaders", "X-Goog-Signature",
                  "OSSAccessKeyId", "Expires", "Signature", "SecurityToken",
                  "AccessKeyId", "X-Obs-Date", "X-Obs-Security-Token",
                  "q-sign-algorithm", "q-ak", "q-sign-time", "q-key-time",
                  "q-header-list", "q-url-param-list", "q-signature",
                  "x-cos-security-token", "ns"));
}

TEST(RequestTest, RequestsDefaultAsTheGoAndRustClients) {
  GetRequest get;
  EXPECT_FALSE(get.filtered_query_params.empty());
  EXPECT_TRUE(get.enable_task_id_based_blob_digest);
  EXPECT_EQ(get.replicas, 2);
  EXPECT_EQ(get.timeout, std::chrono::minutes(10));

  PreheatRequest preheat;
  EXPECT_FALSE(preheat.filtered_query_params.empty());
  EXPECT_TRUE(preheat.enable_task_id_based_blob_digest);
  EXPECT_EQ(preheat.replicas, 2);
  EXPECT_EQ(preheat.timeout, std::chrono::minutes(10));

  PreheatImageRequest preheat_image;
  EXPECT_FALSE(preheat_image.platform.has_value());
  EXPECT_EQ(preheat_image.replicas, 2);
  EXPECT_EQ(preheat_image.concurrent_task_count, 4);
  EXPECT_EQ(preheat_image.timeout, std::chrono::minutes(10));

  StatImageRequest stat_image;
  EXPECT_FALSE(stat_image.platform.has_value());
  EXPECT_FALSE(stat_image.filtered_query_params.empty());
  EXPECT_TRUE(stat_image.enable_task_id_based_blob_digest);
  EXPECT_EQ(stat_image.timeout, std::chrono::minutes(10));
  EXPECT_EQ(stat_image.concurrent_peer_count, 500);

  DeleteRequest del;
  EXPECT_FALSE(del.filtered_query_params.empty());
  EXPECT_TRUE(del.enable_task_id_based_blob_digest);
  EXPECT_EQ(del.replicas, 2);
  EXPECT_EQ(del.timeout, std::chrono::minutes(10));

  DeleteImageRequest delete_image;
  EXPECT_FALSE(delete_image.platform.has_value());
  EXPECT_EQ(delete_image.timeout, std::chrono::minutes(10));
  EXPECT_EQ(delete_image.concurrent_peer_count, 500);
}

TEST(RequestTest, ValidateAcceptsTheDefaultRequests) {
  GetRequest get;
  for (const char* url :
       {"http://example.com/file.txt", "https://example.com/file.txt"}) {
    get.url = url;
    EXPECT_TRUE(Validate(get).ok()) << url;
  }

  PreheatImageRequest preheat_image;
  preheat_image.platform = "linux/amd64";
  EXPECT_TRUE(Validate(PreheatRequest()).ok());
  EXPECT_TRUE(Validate(preheat_image).ok());
  EXPECT_TRUE(Validate(StatImageRequest()).ok());
  EXPECT_TRUE(Validate(DeleteRequest()).ok());
  EXPECT_TRUE(Validate(DeleteImageRequest()).ok());
}

TEST(RequestTest, ValidateRejectsAGetRequestWithoutReplicas) {
  GetRequest request;
  request.url = "http://example.com/file.txt";
  request.replicas = 0;
  ExpectInvalidArgument(Validate(request), "replicas must be positive");
}

TEST(RequestTest, ValidateRejectsAGetRequestWithAnInvalidUrl) {
  for (const char* url :
       {"file:///tmp/file.txt", "ftp://example.com/file.txt",
        "example.com/file.txt", "http://:80/file.txt", "localhost:4001",
        "http://exa mple.com/a", "http://example.com:99999/a"}) {
    GetRequest request;
    request.url = url;
    EXPECT_EQ(Validate(request).code(), absl::StatusCode::kInvalidArgument)
        << url;
  }
}

TEST(RequestTest, ValidateRejectsAPreheatOrDeleteRequestWithoutReplicas) {
  PreheatRequest preheat;
  preheat.replicas = 0;
  ExpectInvalidArgument(Validate(preheat), "replicas must be positive");

  DeleteRequest del;
  del.replicas = 0;
  ExpectInvalidArgument(Validate(del), "replicas must be positive");
}

TEST(RequestTest, ValidateRejectsAnInvalidPreheatImageRequest) {
  PreheatImageRequest platform;
  platform.platform = "linux-amd64";
  PreheatImageRequest replicas;
  replicas.replicas = 0;
  PreheatImageRequest concurrent_task_count;
  concurrent_task_count.concurrent_task_count = 0;
  const std::pair<PreheatImageRequest, const char*> test_cases[] = {
      {platform, "invalid platform format"},
      {replicas, "replicas must be positive"},
      {concurrent_task_count, "concurrent task count must be positive"},
  };

  for (const auto& [request, message] : test_cases) {
    ExpectInvalidArgument(Validate(request), message);
  }
}

TEST(RequestTest, ValidateRejectsAnInvalidStatImageRequest) {
  StatImageRequest platform;
  platform.platform = "linux-amd64";
  StatImageRequest concurrent_peer_count;
  concurrent_peer_count.concurrent_peer_count = 0;
  const std::pair<StatImageRequest, const char*> test_cases[] = {
      {platform, "invalid platform format"},
      {concurrent_peer_count, "concurrent peer count must be positive"},
  };

  for (const auto& [request, message] : test_cases) {
    ExpectInvalidArgument(Validate(request), message);
  }
}

TEST(RequestTest, ValidateRejectsAnInvalidDeleteImageRequest) {
  DeleteImageRequest platform;
  platform.platform = "linux-amd64";
  DeleteImageRequest concurrent_peer_count;
  concurrent_peer_count.concurrent_peer_count = 0;
  const std::pair<DeleteImageRequest, const char*> test_cases[] = {
      {platform, "invalid platform format"},
      {concurrent_peer_count, "concurrent peer count must be positive"},
  };

  for (const auto& [request, message] : test_cases) {
    ExpectInvalidArgument(Validate(request), message);
  }
}

TEST(RequestTest, ProxyHeadersAppliesTheP2PHeaders) {
  GetRequest request;
  request.header = {{"Range", "bytes=0-9"}, {"x-dragonfly-tag", "replaced"}};
  request.piece_length = 4194304;
  request.tag = "tag";
  request.application = "application";
  request.filtered_query_params = {"Expires", "Signature"};
  request.content_for_calculating_task_id = "content";
  request.enable_task_id_based_blob_digest = false;
  request.priority = 3;

  auto headers = ProxyHeaders(request);
  ASSERT_TRUE(headers.ok()) << headers.status();
  EXPECT_THAT(
      *headers,
      ElementsAre(
          Pair("Range", "bytes=0-9"),
          Pair("X-Dragonfly-Application", "application"),
          Pair("X-Dragonfly-Content-For-Calculating-Task-ID", "content"),
          Pair("X-Dragonfly-Enable-Task-ID-Based-Blob-Digest", "false"),
          Pair("X-Dragonfly-Filtered-Query-Params", "Expires,Signature"),
          Pair("X-Dragonfly-Piece-Length", "4194304"),
          Pair("X-Dragonfly-Priority", "3"), Pair("X-Dragonfly-Tag", "tag"),
          Pair("X-Dragonfly-Use-P2P", "true")));

  GetRequest unset;
  unset.filtered_query_params.clear();
  headers = ProxyHeaders(unset);
  ASSERT_TRUE(headers.ok()) << headers.status();
  EXPECT_THAT(
      *headers,
      ElementsAre(Pair("X-Dragonfly-Enable-Task-ID-Based-Blob-Digest", "true"),
                  Pair("X-Dragonfly-Use-P2P", "true")));
}

TEST(RequestTest, ProxyHeadersRejectsAnInvalidHeader) {
  GetRequest tag;
  tag.tag = "bad\r\nvalue";
  GetRequest application;
  application.application = "bad\r\nvalue";
  GetRequest name;
  name.header = {{"Bad Name", "value"}};

  for (const auto& request : {tag, application, name}) {
    EXPECT_EQ(ProxyHeaders(request).status().code(),
              absl::StatusCode::kInvalidArgument);
  }
}

TEST(RequestTest, NormalizePlatformNormalizesTheOsAndTheArchitecture) {
  const std::pair<const char*, const char*> test_cases[] = {
      {"linux/amd64", "linux/amd64"},     {"Linux/AMD64", "linux/amd64"},
      {"linux/x86_64", "linux/amd64"},    {"linux/x86-64", "linux/amd64"},
      {"linux/amd64/v1", "linux/amd64"},  {"linux/arm64", "linux/arm64"},
      {"linux/aarch64", "linux/arm64"},   {"linux/arm64/v8", "linux/arm64"},
      {"linux/arm", "linux/arm"},         {"linux/arm/v7", "linux/arm"},
      {"linux/armhf", "linux/arm"},       {"linux/armel", "linux/arm"},
      {"linux/i386", "linux/386"},        {"macos/arm64", "darwin/arm64"},
      {"windows/amd64", "windows/amd64"},
  };

  for (const auto& [platform, expected] : test_cases) {
    auto normalized = NormalizePlatform(platform);
    ASSERT_TRUE(normalized.ok()) << platform << ": " << normalized.status();
    EXPECT_EQ(*normalized, expected) << platform;
  }
}

TEST(RequestTest, NormalizePlatformRejectsAPlatformThatIsNotOsArch) {
  for (const char* platform :
       {"linux-amd64", "linux", "amd64", "", "/", "linux/", "/amd64",
        "linux/amd64/v8/extra", "linux/*", "linux/amd 64"}) {
    ExpectInvalidArgument(NormalizePlatform(platform).status(),
                          "invalid platform format");
  }
}

}  // namespace
}  // namespace dragonfly::client_request::internal
