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

#include <chrono>
#include <string>

#include "absl/strings/match.h"
#include "src/proxy_impl.h"
#include "src/testing/proxy_fixture.h"

namespace dragonfly::client_request::internal {
namespace {

using testing::BlobUrl;
using ::testing::ElementsAre;
using ::testing::HasSubstr;
using testing::kAmd64;
using testing::kArm64;
using testing::kConfigAmd64;
using testing::kConfigArm64;
using testing::kImage;
using testing::kShared;
using ::testing::SizeIs;

using StatTest = testing::ProxyFixture;

StatImageRequest TestStatImageRequest() {
  StatImageRequest request;
  request.image = kImage;
  request.timeout = std::chrono::seconds(5);
  return request;
}

TEST_F(StatTest, StatImageListsTheLayersEverySeedPeerCached) {
  AddSeedPeers(3);
  fakes_[0]->stat_local_task =
      [](const dfdaemon::v2::StatLocalTaskRequest& request,
         dfdaemon::v2::StatLocalTaskResponse* response) {
        if (absl::EndsWith(kConfigAmd64, request.task_id())) {
          response->mutable_finished_at()->set_seconds(1);
          return grpc::Status::OK;
        }

        return absl::EndsWith(kShared, request.task_id())
                   ? grpc::Status::OK
                   : grpc::Status(grpc::StatusCode::NOT_FOUND,
                                  "task not found");
      };
  fakes_[2]->stat_local_task = [](const dfdaemon::v2::StatLocalTaskRequest&,
                                  dfdaemon::v2::StatLocalTaskResponse*) {
    return grpc::Status(grpc::StatusCode::INTERNAL, "failed");
  };

  StatImageRequest request = TestStatImageRequest();
  request.concurrent_peer_count = 2;
  auto response = proxy_.StatImage(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_THAT(
      response->layers,
      ElementsAre(BlobUrl(kConfigAmd64), BlobUrl(kShared), BlobUrl(kAmd64),
                  BlobUrl(kConfigArm64), BlobUrl(kArm64)));
  ASSERT_THAT(response->peers, SizeIs(1));
  EXPECT_EQ(response->peers[0].hostname, "seed-peer-1");
  EXPECT_EQ(response->peers[0].ip, "127.0.0.1");
  ASSERT_THAT(response->peers[0].cached_layers, SizeIs(2));
  EXPECT_EQ(response->peers[0].cached_layers[0].url, BlobUrl(kConfigAmd64));
  EXPECT_TRUE(response->peers[0].cached_layers[0].is_finished);
  EXPECT_EQ(response->peers[0].cached_layers[1].url, BlobUrl(kShared));
  EXPECT_FALSE(response->peers[0].cached_layers[1].is_finished);
  EXPECT_EQ(fakes_[0]->stat_local_task_calls, 5);
  EXPECT_EQ(fakes_[1]->stat_local_task_calls, 5);
  EXPECT_EQ(fakes_[2]->stat_local_task_calls, 2);
}

TEST_F(StatTest, StatImageStatsOnlyThePlatformGiven) {
  AddSeedPeers(1);
  fakes_[0]->stat_local_task =
      [](const dfdaemon::v2::StatLocalTaskRequest&,
         dfdaemon::v2::StatLocalTaskResponse* response) {
        response->mutable_finished_at()->set_seconds(1);
        return grpc::Status::OK;
      };

  StatImageRequest request = TestStatImageRequest();
  request.platform = "linux/amd64";
  auto response = proxy_.StatImage(request);
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_THAT(response->layers, ElementsAre(BlobUrl(kConfigAmd64),
                                            BlobUrl(kShared), BlobUrl(kAmd64)));
  ASSERT_THAT(response->peers, SizeIs(1));
  EXPECT_THAT(response->peers[0].cached_layers, SizeIs(3));
  EXPECT_EQ(fakes_[0]->stat_local_task_calls, 3);
}

TEST_F(StatTest, StatImageRejectsAnInvalidRequest) {
  AddSeedPeers(1);
  StatImageRequest request = TestStatImageRequest();
  request.concurrent_peer_count = 0;
  EXPECT_EQ(proxy_.StatImage(request).status().code(),
            absl::StatusCode::kInvalidArgument);

  request = TestStatImageRequest();
  request.image = "INVALID IMAGE";
  EXPECT_EQ(proxy_.StatImage(request).status().code(),
            absl::StatusCode::kInvalidArgument);

  request = TestStatImageRequest();
  request.image = "registry.example.com/library/nginx:missing";
  const absl::Status status = proxy_.StatImage(request).status();
  EXPECT_EQ(status.code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("failed to pull image manifest"));
  EXPECT_EQ(fakes_[0]->stat_local_task_calls, 0);
}

TEST_F(StatTest, StatImageFailsWithoutSeedPeers) {
  const absl::Status status = proxy_.StatImage(TestStatImageRequest()).status();
  EXPECT_EQ(status.code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(status.message()),
              HasSubstr("failed to select seed peers"));
}

}  // namespace
}  // namespace dragonfly::client_request::internal
