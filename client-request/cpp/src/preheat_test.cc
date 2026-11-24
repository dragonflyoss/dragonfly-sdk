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

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <atomic>
#include <chrono>
#include <set>
#include <string>
#include <utility>
#include <vector>

#include "absl/strings/match.h"
#include "dragonfly/client_request/errors.h"
#include "src/proxy_impl.h"
#include "src/testing/proxy_fixture.h"

namespace dragonfly::client_request::internal {
namespace {

using testing::BlobUrl;
using ::testing::ElementsAre;
using ::testing::HasSubstr;
using testing::kArm64;
using testing::kConfigAmd64;
using testing::kImage;
using testing::kOciIndex;
using testing::kShared;
using ::testing::Pair;
using ::testing::SizeIs;

using PreheatTest = testing::ProxyFixture;

PreheatRequest TestPreheatRequest() {
  PreheatRequest request;
  request.url = "https://example.com/file.txt";
  request.timeout = std::chrono::seconds(5);
  return request;
}

PreheatImageRequest TestPreheatImageRequest() {
  PreheatImageRequest request;
  request.image = kImage;
  request.replicas = 1;
  request.timeout = std::chrono::seconds(5);
  return request;
}

TEST_F(PreheatTest, PreheatDownloadsTheTaskOnItsReplicas) {
  AddSeedPeers(3);
  PreheatRequest request = TestPreheatRequest();
  request.header = {{"X-A", "1"}, {"X-A", "2"}, {"X-B", "3"}};
  request.piece_length = 4194304;
  request.tag = "tag";
  request.application = "application";
  request.filtered_query_params = {"Signature"};
  ASSERT_TRUE(proxy_.Preheat(request).ok());

  const auto requests = DownloadTaskRequests();
  ASSERT_THAT(requests, SizeIs(2));
  for (const auto& download_task_request : requests) {
    const common::v2::Download& download = download_task_request.download();
    EXPECT_EQ(download.url(), "https://example.com/file.txt");
    EXPECT_EQ(download.type(), common::v2::TaskType::STANDARD);
    EXPECT_EQ(download.priority(), common::v2::Priority::LEVEL6);
    EXPECT_EQ(download.scheduling_policy(),
              common::v2::SchedulingPolicy::ALWAYS);
    EXPECT_THAT(download.filtered_query_params(), ElementsAre("Signature"));
    EXPECT_EQ(download.request_header().at("X-A"), "2");
    EXPECT_EQ(download.request_header().at("X-B"), "3");
    EXPECT_EQ(download.piece_length(), 4194304U);
    EXPECT_EQ(download.tag(), "tag");
    EXPECT_EQ(download.application(), "application");
    EXPECT_EQ(download.timeout().seconds(), 5);
    EXPECT_TRUE(download.enable_task_id_based_blob_digest());
    EXPECT_FALSE(download.has_content_for_calculating_task_id());
    EXPECT_FALSE(download.has_image());
  }
}

TEST_F(PreheatTest, PreheatCarriesThePriorityAndTheContentOfTheTaskId) {
  AddSeedPeers(1);
  PreheatRequest request = TestPreheatRequest();
  request.replicas = 1;
  request.priority = 3;
  request.content_for_calculating_task_id = "content";
  request.timeout = std::chrono::milliseconds(1500);
  ASSERT_TRUE(proxy_.Preheat(request).ok());

  const common::v2::Download download =
      fakes_[0]->download_task_request().download();
  EXPECT_EQ(download.priority(), common::v2::Priority::LEVEL3);
  EXPECT_EQ(download.content_for_calculating_task_id(), "content");
  EXPECT_EQ(download.timeout().seconds(), 1);
  EXPECT_EQ(download.timeout().nanos(), 500000000);
}

TEST_F(PreheatTest, PreheatAddressesAllSeedPeersWithTheScope) {
  AddSeedPeers(3);
  PreheatRequest request = TestPreheatRequest();
  request.scope = Scope::kAllSeedPeers;
  ASSERT_TRUE(proxy_.Preheat(request).ok());
  for (const auto& fake : fakes_) {
    EXPECT_EQ(fake->download_task_calls, 1);
  }
}

TEST_F(PreheatTest, PreheatFailsWithInsufficientSeedPeers) {
  AddSeedPeers(1);
  absl::Status status = proxy_.Preheat(TestPreheatRequest());
  EXPECT_EQ(status.code(), absl::StatusCode::kInternal);
  EXPECT_EQ(status.message(),
            "insufficient seed peers for 2 replicas, 1 available");
  EXPECT_EQ(DownloadTaskCalls(), 0);
}

TEST_F(PreheatTest, PreheatFailsWithoutSeedPeersOrWithAnInvalidRequest) {
  absl::Status status = proxy_.Preheat(TestPreheatRequest());
  EXPECT_EQ(status.code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("failed to select seed peers"));

  PreheatRequest request = TestPreheatRequest();
  request.replicas = 0;
  EXPECT_EQ(proxy_.Preheat(request).code(), absl::StatusCode::kInvalidArgument);
}

TEST_F(PreheatTest, PreheatRetriesOnlyTransientFailures) {
  struct TestCase {
    grpc::StatusCode code;
    std::string details;
    absl::StatusCode expected_code;
    int expected_calls;
  };

  const TestCase test_cases[] = {
      {grpc::StatusCode::UNAVAILABLE, "", absl::StatusCode::kUnavailable, 2},
      {grpc::StatusCode::INTERNAL, "", absl::StatusCode::kInternal, 2},
      {grpc::StatusCode::RESOURCE_EXHAUSTED, "",
       absl::StatusCode::kResourceExhausted, 2},
      {grpc::StatusCode::INVALID_ARGUMENT, "",
       absl::StatusCode::kInvalidArgument, 1},
      {grpc::StatusCode::NOT_FOUND, "", absl::StatusCode::kNotFound, 1},
      {grpc::StatusCode::INTERNAL,
       R"({"message":"not found","header":{"X-A":"b"},"status_code":404})",
       absl::StatusCode::kNotFound, 1},
      {grpc::StatusCode::INTERNAL, R"({"message":"busy","status_code":503})",
       absl::StatusCode::kUnavailable, 2},
      {grpc::StatusCode::INTERNAL, R"({"message":"no status code"})",
       absl::StatusCode::kInternal, 2},
  };

  AddSeedPeers(1);
  PreheatRequest request = TestPreheatRequest();
  request.replicas = 1;
  for (const auto& test_case : test_cases) {
    fakes_[0]->download_task_calls = 0;
    fakes_[0]->download_task = [&test_case] {
      return grpc::Status(test_case.code, "failed", test_case.details);
    };

    absl::Status status = proxy_.Preheat(request);
    EXPECT_EQ(status.code(), test_case.expected_code) << status;
    EXPECT_EQ(fakes_[0]->download_task_calls, test_case.expected_calls)
        << status;
  }
}

TEST_F(PreheatTest, PreheatSucceedsAfterATransientFailureAndCarriesTheAnswer) {
  AddSeedPeers(1);
  PreheatRequest request = TestPreheatRequest();
  request.replicas = 1;
  fakes_[0]->download_task = [this] {
    return fakes_[0]->download_task_calls == 1
               ? grpc::Status(grpc::StatusCode::UNAVAILABLE, "down")
               : grpc::Status::OK;
  };
  EXPECT_TRUE(proxy_.Preheat(request).ok());
  EXPECT_EQ(fakes_[0]->download_task_calls, 2);

  fakes_[0]->download_task = [] {
    return grpc::Status(
        grpc::StatusCode::INTERNAL, "failed",
        R"({"message":"not found","header":{"X-A":"b"},"status_code":404})");
  };
  auto detail = GetErrorDetail(proxy_.Preheat(request));
  ASSERT_TRUE(detail.has_value());
  EXPECT_EQ(detail->type, ErrorType::kBackend);
  EXPECT_EQ(detail->status_code, 404);
  EXPECT_EQ(detail->message, "not found");
  EXPECT_THAT(detail->header, ElementsAre(Pair("X-A", "b")));
}

TEST_F(PreheatTest, PreheatImagePreheatsEveryManifestAndBlobOfEveryPlatform) {
  AddSeedPeers(1);
  ASSERT_TRUE(proxy_.PreheatImage(TestPreheatImageRequest()).ok());

  std::multiset<std::pair<std::string, std::string>> targets;
  int manifests = 0;
  for (const auto& request : DownloadTaskRequests()) {
    const common::v2::Download& download = request.download();
    targets.emplace(download.url(), download.image().platform());
    EXPECT_EQ(download.image().reference(), kImage);
    EXPECT_EQ(download.request_header().at("Authorization"), "Bearer abc");
    if (absl::StrContains(download.url(), "/manifests/")) {
      ++manifests;
      EXPECT_THAT(download.request_header().at("Accept"), HasSubstr(kOciIndex));
      EXPECT_THAT(download.url(), HasSubstr("/manifests/sha256:"));
    } else {
      EXPECT_EQ(download.request_header().count("Accept"), 0U);
    }
  }

  EXPECT_EQ(manifests, 2);
  EXPECT_THAT(targets, SizeIs(8));
  EXPECT_EQ(targets.count({BlobUrl(kShared), "linux/amd64"}), 1U);
  EXPECT_EQ(targets.count({BlobUrl(kShared), "linux/arm64"}), 1U);
  EXPECT_EQ(targets.count({BlobUrl(kConfigAmd64), "linux/amd64"}), 1U);
  EXPECT_EQ(targets.count({BlobUrl(kArm64), "linux/arm64"}), 1U);
}

TEST_F(PreheatTest, PreheatImagePreheatsOnlyThePlatformGiven) {
  AddSeedPeers(1);
  PreheatImageRequest request = TestPreheatImageRequest();
  request.platform = "linux/aarch64";
  request.tag = "tag";
  request.priority = 3;
  ASSERT_TRUE(proxy_.PreheatImage(request).ok());

  const auto requests = DownloadTaskRequests();
  ASSERT_THAT(requests, SizeIs(4));
  for (const auto& download_task_request : requests) {
    const common::v2::Download& download = download_task_request.download();
    EXPECT_EQ(download.image().platform(), "linux/arm64");
    EXPECT_EQ(download.tag(), "tag");
    EXPECT_EQ(download.priority(), common::v2::Priority::LEVEL3);
  }
}

TEST_F(PreheatTest, PreheatImageRejectsAnInvalidRequest) {
  AddSeedPeers(1);
  PreheatImageRequest request = TestPreheatImageRequest();
  request.image = "invalid image reference!!";
  absl::Status status = proxy_.PreheatImage(request);
  EXPECT_EQ(status.code(), absl::StatusCode::kInvalidArgument);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("invalid image reference"));

  request = TestPreheatImageRequest();
  request.platform = "linux-amd64";
  status = proxy_.PreheatImage(request);
  EXPECT_EQ(status.code(), absl::StatusCode::kInvalidArgument);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("invalid platform format"));

  request = TestPreheatImageRequest();
  request.concurrent_task_count = 0;
  EXPECT_EQ(proxy_.PreheatImage(request).code(),
            absl::StatusCode::kInvalidArgument);
  EXPECT_EQ(DownloadTaskCalls(), 0);
}

TEST_F(PreheatTest, PreheatImageRetriesATransientFailureOfTheRegistry) {
  std::atomic<int> requests{0};
  ProxyImpl flaky(
      RetryPolicy(), std::chrono::seconds(5),
      [this, &requests](const std::string& url,
                        const Header& header) -> absl::StatusOr<HttpResult> {
        if (++requests == 1) {
          return absl::InternalError("error sending request");
        }

        return registry_.getter()(url, header);
      });
  AddSeedPeers(1);
  flaky.selector().Update({fakes_[0]->seed_peer()});
  EXPECT_TRUE(flaky.PreheatImage(TestPreheatImageRequest()).ok());
  EXPECT_EQ(DownloadTaskCalls(), 8);

  ProxyImpl unreachable(
      RetryPolicy(), std::chrono::seconds(5),
      [](const std::string&, const Header&) -> absl::StatusOr<HttpResult> {
        return absl::InternalError("error sending request");
      });
  unreachable.selector().Update({fakes_[0]->seed_peer()});
  absl::Status status = unreachable.PreheatImage(TestPreheatImageRequest());
  EXPECT_EQ(status.code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("failed to authenticate with registry"));
}

TEST_F(PreheatTest, PreheatImageFailsWhenTheManifestIsMissing) {
  AddSeedPeers(1);
  PreheatImageRequest request = TestPreheatImageRequest();
  request.image = "registry.example.com/library/nginx:missing";
  absl::Status status = proxy_.PreheatImage(request);
  EXPECT_EQ(status.code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("failed to pull image manifest"));

  request = TestPreheatImageRequest();
  request.platform = "windows/amd64";
  status = proxy_.PreheatImage(request);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("no matching manifest for platform windows/amd64"));
  EXPECT_EQ(DownloadTaskCalls(), 0);
}

}  // namespace
}  // namespace dragonfly::client_request::internal
