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

#include <google/protobuf/util/json_util.h>

#include <algorithm>
#include <cstddef>
#include <utility>

#include "absl/strings/str_cat.h"
#include "absl/strings/str_join.h"
#include "dfdaemon.grpc.pb.h"
#include "errordetails.pb.h"
#include "src/errors_internal.h"
#include "src/grpc_util.h"
#include "src/id_generator.h"
#include "src/net.h"
#include "src/parallel.h"
#include "src/proxy_impl.h"
#include "src/request_internal.h"

namespace dragonfly::client_request::internal {
namespace {

// Returns the download task request that preheats the file of the request,
// its task recorded by the image when one is given.
dfdaemon::v2::DownloadTaskRequest NewDownloadTaskRequest(
    const PreheatRequest& request, const std::optional<TaskImage>& image) {
  dfdaemon::v2::DownloadTaskRequest download_task_request;
  common::v2::Download* download = download_task_request.mutable_download();
  download->set_url(request.url);
  download->set_type(common::v2::TaskType::STANDARD);
  download->set_priority(
      request.priority ? static_cast<common::v2::Priority>(*request.priority)
                       : common::v2::Priority::LEVEL6);
  for (const auto& param : request.filtered_query_params) {
    download->add_filtered_query_params(param);
  }

  // The last value wins for a header given more than once.
  for (const auto& header : request.header) {
    (*download->mutable_request_header())[header.first] = header.second;
  }

  if (request.piece_length) {
    download->set_piece_length(*request.piece_length);
  }

  download->mutable_timeout()->set_seconds(request.timeout.count() / 1000);
  download->mutable_timeout()->set_nanos(
      static_cast<std::int32_t>(request.timeout.count() % 1000) * 1000000);
  if (auto local_ip = LocalIp()) {
    download->set_remote_ip(*local_ip);
  }

  download->set_enable_task_id_based_blob_digest(
      request.enable_task_id_based_blob_digest);
  download->set_scheduling_policy(common::v2::SchedulingPolicy::ALWAYS);
  if (!request.tag.empty()) {
    download->set_tag(request.tag);
  }

  if (!request.application.empty()) {
    download->set_application(request.application);
  }

  if (!request.content_for_calculating_task_id.empty()) {
    download->set_content_for_calculating_task_id(
        request.content_for_calculating_task_id);
  }

  if (image) {
    download->mutable_image()->set_reference(image->reference);
    if (image->platform) {
      download->mutable_image()->set_platform(*image->platform);
    }
  }

  return download_task_request;
}

}  // namespace

absl::Status ProxyImpl::Preheat(const PreheatRequest& request) const {
  return Preheat(request, std::nullopt);
}

absl::Status ProxyImpl::Preheat(const PreheatRequest& request,
                                const std::optional<TaskImage>& image) const {
  if (auto status = Validate(request); !status.ok()) {
    return status;
  }

  // Generate task id for selecting seed peer.
  auto task_id = TaskId(request);
  if (!task_id.ok()) {
    return task_id.status();
  }

  // Select seed peers for downloading.
  auto seed_peers = SelectSeedPeers(*task_id, request.scope, request.replicas);
  if (!seed_peers.ok()) {
    return seed_peers.status();
  }

  if (request.scope == Scope::kDefault &&
      seed_peers->size() < static_cast<std::size_t>(request.replicas)) {
    return absl::InternalError(absl::StrCat("insufficient seed peers for ",
                                            request.replicas, " replicas, ",
                                            seed_peers->size(), " available"));
  }

  // Trigger every replica seed peer to download the task concurrently and
  // wait for the download tasks to finish.
  const dfdaemon::v2::DownloadTaskRequest download_task_request =
      NewDownloadTaskRequest(request, image);
  return ParallelFor(
      seed_peers->size(), seed_peers->size(), [&](std::size_t i) {
        return DownloadTask((*seed_peers)[i], *task_id, download_task_request,
                            request.timeout);
      });
}

absl::Status ProxyImpl::PreheatImage(const PreheatImageRequest& request) const {
  if (auto status = Validate(request); !status.ok()) {
    return status;
  }

  auto resolved = ResolveImage(request.image, request.username,
                               request.password, request.platform);
  if (!resolved.ok()) {
    return resolved.status();
  }

  // Collect the manifests and blobs of the matched platforms. A blob shared
  // by several platforms is preheated for each of them.
  struct Target {
    std::string url;
    bool is_manifest;
    TaskImage image;
  };

  std::vector<Target> targets;
  for (const auto& manifest : resolved->manifests) {
    TaskImage image;
    image.reference = resolved->reference.ToString();
    if (!manifest.platform.empty()) {
      image.platform = manifest.platform;
    }

    auto has_target = [&](const std::string& url) {
      return std::any_of(
          targets.begin(), targets.end(), [&](const Target& target) {
            return target.url == url && target.image.platform == image.platform;
          });
    };
    if (has_target(manifest.url)) {
      continue;
    }

    targets.push_back({manifest.url, true, image});
    for (const auto& blob_url : manifest.blob_urls) {
      if (!has_target(blob_url)) {
        targets.push_back({blob_url, false, image});
      }
    }
  }

  // Preheat the manifests and blobs concurrently, limited by the concurrent
  // task count.
  return ParallelFor(
      targets.size(), request.concurrent_task_count, [&](std::size_t i) {
        const Target& target = targets[i];
        PreheatRequest preheat_request;
        preheat_request.url = target.url;
        preheat_request.piece_length = request.piece_length;
        preheat_request.tag = request.tag;
        preheat_request.application = request.application;
        preheat_request.filtered_query_params = request.filtered_query_params;
        preheat_request.enable_task_id_based_blob_digest =
            request.enable_task_id_based_blob_digest;
        preheat_request.priority = request.priority;
        preheat_request.replicas = request.replicas;
        preheat_request.scope = request.scope;
        preheat_request.timeout = request.timeout;

        // Authorize the target when it starts, since the token of the
        // registry expires during a long preheat.
        std::string authorization;
        absl::Status status = retry_.Run([&] {
          auto result = resolved->client->Authorization();
          if (!result.ok()) {
            return result.status();
          }

          authorization = std::move(*result);
          return absl::OkStatus();
        });
        if (!status.ok()) {
          return status;
        }

        // Accept the manifest media types for a manifest, so the registry
        // returns the exact representation matching the digest.
        if (target.is_manifest) {
          preheat_request.header.emplace(
              "Accept", absl::StrJoin(ManifestMediaTypes(), ","));
        }

        if (!authorization.empty()) {
          preheat_request.header.emplace("Authorization", authorization);
        }

        return Preheat(preheat_request, target.image);
      });
}

absl::Status ProxyImpl::DownloadTask(
    const SeedPeer& seed_peer, const std::string& task_id,
    const dfdaemon::v2::DownloadTaskRequest& request,
    std::chrono::milliseconds timeout) const {
  return retry_.Run([&] {
    grpc::ClientContext context;
    SetTimeout(&context, timeout);
    auto stub = NewDfdaemonStub(seed_peer);
    auto reader = stub->DownloadTask(&context, request);
    dfdaemon::v2::DownloadTaskResponse response;
    while (reader->Read(&response)) {
    }

    const grpc::Status status = reader->Finish();
    if (status.ok()) {
      return absl::OkStatus();
    }

    // The dfdaemon reports a failure of the source as the JSON of the backend
    // error in the details of the status, so a definitive answer such as 404
    // is not retried.
    errordetails::v2::Backend backend;
    google::protobuf::util::JsonParseOptions options;
    options.ignore_unknown_fields = true;
    if (google::protobuf::util::JsonStringToMessage(status.error_details(),
                                                    &backend, options)
            .ok() &&
        backend.has_status_code()) {
      ErrorDetail detail;
      detail.type = ErrorType::kBackend;
      detail.status_code = backend.status_code();
      detail.message = backend.message();
      for (const auto& header : backend.header()) {
        detail.header.emplace(header.first, header.second);
      }

      return AnswerError(detail);
    }

    return FromGrpcStatus(status,
                          absl::StrCat("failed to download task ", task_id));
  });
}

}  // namespace dragonfly::client_request::internal
