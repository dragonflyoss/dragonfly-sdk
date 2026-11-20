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

#include "src/proxy_impl.h"
#include "src/testing/proxy_fixture.h"

namespace dragonfly::client_request::internal {
namespace {

using ::testing::ElementsAre;
using ::testing::IsEmpty;
using ::testing::SizeIs;

using DeleteTest = testing::ProxyFixture;

DeleteImageRequest TestDeleteImageRequest() {
  DeleteImageRequest request;
  request.image = "ghcr.io/dragonflyoss/client:v1";
  request.timeout = std::chrono::seconds(5);
  return request;
}

TEST_F(DeleteTest, DeleteDeletesTheTaskFromItsReplicas) {
  AddSeedPeers(3);
  DeleteRequest request;
  request.url = "https://example.com/file.txt";
  request.timeout = std::chrono::seconds(5);
  ASSERT_TRUE(proxy_.Delete(request).ok());

  int delete_task_calls = 0;
  for (const auto& fake : fakes_) {
    delete_task_calls += fake->delete_task_calls;
  }

  EXPECT_EQ(delete_task_calls, 2);
}

TEST_F(DeleteTest, DeleteRetriesOnlyTransientFailures) {
  AddSeedPeers(3);
  DeleteRequest request;
  request.url = "https://example.com/file.txt";
  request.timeout = std::chrono::seconds(5);
  request.scope = Scope::kAllSeedPeers;
  fakes_[1]->delete_task = [] {
    return grpc::Status(grpc::StatusCode::NOT_FOUND, "task not found");
  };
  fakes_[2]->delete_task = [this] {
    return fakes_[2]->delete_task_calls == 1
               ? grpc::Status(grpc::StatusCode::UNAVAILABLE, "down")
               : grpc::Status::OK;
  };
  EXPECT_TRUE(proxy_.Delete(request).ok());
  EXPECT_EQ(fakes_[0]->delete_task_calls, 1);
  EXPECT_EQ(fakes_[1]->delete_task_calls, 1);
  EXPECT_EQ(fakes_[2]->delete_task_calls, 2);

  fakes_[0]->delete_task = [] {
    return grpc::Status(grpc::StatusCode::PERMISSION_DENIED, "denied");
  };
  EXPECT_EQ(proxy_.Delete(request).code(), absl::StatusCode::kPermissionDenied);
  EXPECT_EQ(fakes_[0]->delete_task_calls, 2);

  request.replicas = 0;
  EXPECT_EQ(proxy_.Delete(request).code(), absl::StatusCode::kInvalidArgument);
}

TEST_F(DeleteTest, DeleteImageListsTheTasksEverySeedPeerDeleted) {
  AddSeedPeers(2);
  fakes_[0]->delete_image = [](dfdaemon::v2::DeleteImageResponse* response) {
    response->add_deleted_task_ids("task-1");
    response->add_deleted_task_ids("task-2");
    response->add_retained_task_ids("task-3");
    return grpc::Status::OK;
  };
  fakes_[1]->delete_image = [](dfdaemon::v2::DeleteImageResponse*) {
    return grpc::Status(grpc::StatusCode::INTERNAL, "failed");
  };

  DeleteImageRequest request = TestDeleteImageRequest();
  request.platform = "linux/aarch64";
  auto response = proxy_.DeleteImage(request);
  ASSERT_TRUE(response.ok()) << response.status();
  ASSERT_THAT(response->peers, SizeIs(1));
  EXPECT_EQ(response->peers[0].hostname, "seed-peer-1");
  EXPECT_EQ(response->peers[0].ip, "127.0.0.1");
  EXPECT_THAT(response->peers[0].deleted_task_ids,
              ElementsAre("task-1", "task-2"));
  EXPECT_THAT(response->peers[0].retained_task_ids, ElementsAre("task-3"));
  EXPECT_EQ(fakes_[1]->delete_image_calls, 2);
  EXPECT_EQ(fakes_[0]->delete_image_request().url(),
            "https://ghcr.io/v2/dragonflyoss/client/manifests/v1");
  EXPECT_EQ(fakes_[0]->delete_image_request().platform(), "linux/arm64");

  fakes_[0]->delete_image = fakes_[1]->delete_image;
  response = proxy_.DeleteImage(TestDeleteImageRequest());
  ASSERT_TRUE(response.ok()) << response.status();
  EXPECT_THAT(response->peers, IsEmpty());
  EXPECT_FALSE(fakes_[0]->delete_image_request().has_platform());
}

TEST_F(DeleteTest, DeleteImageRejectsAnInvalidRequest) {
  AddSeedPeers(1);
  DeleteImageRequest request = TestDeleteImageRequest();
  request.image = "INVALID IMAGE";
  EXPECT_EQ(proxy_.DeleteImage(request).status().code(),
            absl::StatusCode::kInvalidArgument);

  request = TestDeleteImageRequest();
  request.platform = "linux-amd64";
  EXPECT_EQ(proxy_.DeleteImage(request).status().code(),
            absl::StatusCode::kInvalidArgument);

  request = TestDeleteImageRequest();
  request.concurrent_peer_count = 0;
  EXPECT_EQ(proxy_.DeleteImage(request).status().code(),
            absl::StatusCode::kInvalidArgument);
  EXPECT_EQ(fakes_[0]->delete_image_calls, 0);
}

}  // namespace
}  // namespace dragonfly::client_request::internal
