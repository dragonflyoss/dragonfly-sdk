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

#include <cstddef>
#include <utility>

#include "absl/strings/str_cat.h"
#include "dfdaemon.grpc.pb.h"
#include "src/grpc_util.h"
#include "src/id_generator.h"
#include "src/net.h"
#include "src/parallel.h"
#include "src/proxy_impl.h"
#include "src/request_internal.h"

namespace dragonfly::client_request::internal {

absl::Status ProxyImpl::Delete(const DeleteRequest& request) const {
  if (auto status = Validate(request); !status.ok()) {
    return status;
  }

  // Generate task id for selecting seed peer.
  auto task_id = TaskId(request);
  if (!task_id.ok()) {
    return task_id.status();
  }

  // Select seed peers serving the task.
  auto seed_peers = SelectSeedPeers(*task_id, request.scope, request.replicas);
  if (!seed_peers.ok()) {
    return seed_peers.status();
  }

  // Construct the delete task request.
  dfdaemon::v2::DeleteTaskRequest delete_task_request;
  delete_task_request.set_task_id(*task_id);
  if (auto local_ip = LocalIp()) {
    delete_task_request.set_remote_ip(*local_ip);
  }

  // Delete the task from every replica seed peer concurrently, retrying a
  // transient failure on the same seed peer. A NotFound answer counts as
  // deleted, aligned with the delete task job of the scheduler.
  return ParallelFor(
      seed_peers->size(), seed_peers->size(), [&](std::size_t i) {
        return retry_.Run([&] {
          grpc::ClientContext context;
          SetTimeout(&context, request.timeout);
          google::protobuf::Empty response;
          const grpc::Status status =
              NewDfdaemonStub((*seed_peers)[i])
                  ->DeleteTask(&context, delete_task_request, &response);
          if (!status.ok() &&
              status.error_code() != grpc::StatusCode::NOT_FOUND) {
            return FromGrpcStatus(
                status, absl::StrCat("failed to delete task ", *task_id));
          }

          return absl::OkStatus();
        });
      });
}

absl::StatusOr<DeleteImageResponse> ProxyImpl::DeleteImage(
    const DeleteImageRequest& request) const {
  if (auto status = Validate(request); !status.ok()) {
    return status;
  }

  auto reference = ParseImage(request.image);
  if (!reference.ok()) {
    return reference.status();
  }

  // Select all seed peers.
  auto seed_peers = SelectSeedPeers("", Scope::kAllSeedPeers, 0);
  if (!seed_peers.ok()) {
    return seed_peers.status();
  }

  // Construct the delete image request, the platform is normalized as the
  // preheat records it.
  dfdaemon::v2::DeleteImageRequest delete_image_request;
  delete_image_request.set_url(reference->ManifestUrl());
  if (request.platform) {
    auto platform = NormalizePlatform(*request.platform);
    if (!platform.ok()) {
      return platform.status();
    }

    delete_image_request.set_platform(*platform);
  }

  if (auto local_ip = LocalIp()) {
    delete_image_request.set_remote_ip(*local_ip);
  }

  // Delete the image from every seed peer concurrently, retrying a transient
  // failure on the same seed peer. A seed peer that fails is left out of the
  // response.
  std::vector<std::optional<PeerDeletedImage>> peers(seed_peers->size());
  (void)ParallelFor(
      seed_peers->size(), request.concurrent_peer_count, [&](std::size_t i) {
        const SeedPeer& seed_peer = (*seed_peers)[i];
        dfdaemon::v2::DeleteImageResponse delete_image_response;
        absl::Status status = retry_.Run([&] {
          grpc::ClientContext context;
          SetTimeout(&context, request.timeout);
          const grpc::Status deleted = NewDfdaemonStub(seed_peer)->DeleteImage(
              &context, delete_image_request, &delete_image_response);
          if (!deleted.ok()) {
            return FromGrpcStatus(deleted,
                                  absl::StrCat("failed to delete image ",
                                               delete_image_request.url()));
          }

          return absl::OkStatus();
        });

        if (status.ok()) {
          peers[i] = PeerDeletedImage{
              seed_peer.ip,
              seed_peer.hostname,
              {delete_image_response.deleted_task_ids().begin(),
               delete_image_response.deleted_task_ids().end()},
              {delete_image_response.retained_task_ids().begin(),
               delete_image_response.retained_task_ids().end()}};
        }

        return absl::OkStatus();
      });

  DeleteImageResponse response;
  for (auto& peer : peers) {
    if (peer) {
      response.peers.push_back(std::move(*peer));
    }
  }

  return response;
}

}  // namespace dragonfly::client_request::internal
