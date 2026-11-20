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

#include <algorithm>
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

absl::StatusOr<StatImageResponse> ProxyImpl::StatImage(
    const StatImageRequest& request) const {
  if (auto status = Validate(request); !status.ok()) {
    return status;
  }

  auto resolved = ResolveImage(request.image, request.username,
                               request.password, request.platform);
  if (!resolved.ok()) {
    return resolved.status();
  }

  // Collect the layers of the matched platforms with the ids of their tasks.
  // A layer shared by several platforms is stated once.
  StatImageResponse response;
  std::vector<std::string> task_ids;
  for (const auto& manifest : resolved->manifests) {
    for (const auto& blob_url : manifest.blob_urls) {
      if (std::find(response.layers.begin(), response.layers.end(), blob_url) !=
          response.layers.end()) {
        continue;
      }

      auto task_id = TaskId(blob_url, request.piece_length, request.tag,
                            request.application, request.filtered_query_params,
                            "", request.enable_task_id_based_blob_digest);
      if (!task_id.ok()) {
        return task_id.status();
      }

      response.layers.push_back(blob_url);
      task_ids.push_back(std::move(*task_id));
    }
  }

  // Select all seed peers.
  auto seed_peers = SelectSeedPeers("", Scope::kAllSeedPeers, 0);
  if (!seed_peers.ok()) {
    return seed_peers.status();
  }

  // Stat the layers on every seed peer concurrently. A seed peer that fails
  // or has cached none is left out of the response.
  const std::optional<std::string> local_ip = LocalIp();
  std::vector<std::optional<PeerStatedImage>> peers(seed_peers->size());
  (void)ParallelFor(
      seed_peers->size(), request.concurrent_peer_count, [&](std::size_t i) {
        const SeedPeer& seed_peer = (*seed_peers)[i];
        std::vector<Layer> cached_layers;
        absl::Status status = retry_.Run([&] {
          cached_layers.clear();
          auto stub = NewDfdaemonStub(seed_peer);
          for (std::size_t j = 0; j < task_ids.size(); ++j) {
            grpc::ClientContext context;
            SetTimeout(&context, request.timeout);
            dfdaemon::v2::StatLocalTaskRequest stat_local_task_request;
            stat_local_task_request.set_task_id(task_ids[j]);
            if (local_ip) {
              stat_local_task_request.set_remote_ip(*local_ip);
            }

            // A NotFound answer means the seed peer has not cached the layer.
            dfdaemon::v2::StatLocalTaskResponse stat_local_task_response;
            const grpc::Status stat = stub->StatLocalTask(
                &context, stat_local_task_request, &stat_local_task_response);
            if (stat.error_code() == grpc::StatusCode::NOT_FOUND) {
              continue;
            }

            if (!stat.ok()) {
              return FromGrpcStatus(
                  stat, absl::StrCat("failed to stat task ", task_ids[j]));
            }

            cached_layers.push_back(
                {response.layers[j],
                 stat_local_task_response.has_finished_at()});
          }

          return absl::OkStatus();
        });

        if (status.ok() && !cached_layers.empty()) {
          peers[i] = PeerStatedImage{seed_peer.ip, seed_peer.hostname,
                                     std::move(cached_layers)};
        }

        return absl::OkStatus();
      });

  for (auto& peer : peers) {
    if (peer) {
      response.peers.push_back(std::move(*peer));
    }
  }

  return response;
}

}  // namespace dragonfly::client_request::internal
