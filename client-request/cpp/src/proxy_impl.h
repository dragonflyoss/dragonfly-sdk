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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_PROXY_IMPL_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_PROXY_IMPL_H_

#include <chrono>
#include <memory>
#include <optional>
#include <ostream>
#include <string>
#include <vector>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"
#include "dragonfly/client_request/proxy.h"
#include "dragonfly/client_request/request.h"
#include "src/get.h"
#include "src/oci.h"
#include "src/retry.h"
#include "src/selector.h"

namespace dfdaemon::v2 {
class DownloadTaskRequest;
}  // namespace dfdaemon::v2

namespace dragonfly::client_request::internal {

/// The image a preheated task is recorded by, for the delete of the image.
struct TaskImage {
  /// The reference of the image, such as "docker.io/library/nginx:latest".
  std::string reference;

  /// The platform as "os/arch", all the platforms when unset.
  std::optional<std::string> platform;
};

/// Sends the requests via the seed peers the consistent hash ring selects:
/// those of the scheduler for a Proxy, the given ones for a
/// ProxyWithSeedPeers. It is thread-safe.
class ProxyImpl {
 public:
  /// Creates a proxy without any seed peer, reaching the registries of the
  /// images through the getter.
  ProxyImpl(RetryPolicy retry, std::chrono::milliseconds read_timeout,
            HttpGetter registry_getter);

  /// Returns the selector of the seed peers.
  SeedPeerSelector& selector() { return *selector_; }

  /// Sends a GET request via the seed peers, see Request::Get.
  absl::StatusOr<GetResponse> Get(const GetRequest& request) const;

  /// Sends a GET request via the seed peers, see Request::GetInto.
  absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                      std::ostream& output) const;

  /// Preheats a file, see Request::Preheat.
  absl::Status Preheat(const PreheatRequest& request) const;

  /// Preheats an OCI image, see Request::PreheatImage.
  absl::Status PreheatImage(const PreheatImageRequest& request) const;

  /// Reports which seed peers hold which layers of an OCI image, see
  /// Request::StatImage.
  absl::StatusOr<StatImageResponse> StatImage(
      const StatImageRequest& request) const;

  /// Deletes a preheated file, see Request::Delete.
  absl::Status Delete(const DeleteRequest& request) const;

  /// Deletes a preheated OCI image, see Request::DeleteImage.
  absl::StatusOr<DeleteImageResponse> DeleteImage(
      const DeleteImageRequest& request) const;

  /// Looks up the proxy endpoints of the seed peers serving the request, see
  /// Request::LookupEndpoints.
  absl::StatusOr<std::vector<std::string>> LookupEndpoints(
      const GetRequest& request) const;

 private:
  /// The manifests of an image with the client that authorizes their
  /// downloads.
  struct ResolvedImage {
    Reference reference;
    std::unique_ptr<RegistryClient> client;
    std::vector<Manifest> manifests;
  };

  /// Selects the seed peers the scope addresses for the task: its replicas,
  /// or all seed peers.
  absl::StatusOr<std::vector<SeedPeer>> SelectSeedPeers(
      absl::string_view task_id, Scope scope, int replicas) const;

  /// Resolves the manifests of the image, all the platforms unless one is
  /// given, retrying a transient failure of the registry.
  absl::StatusOr<ResolvedImage> ResolveImage(
      const std::string& image, const std::string& username,
      const std::string& password,
      const std::optional<std::string>& platform) const;

  /// Preheats a file, recorded by the image when one is given.
  absl::Status Preheat(const PreheatRequest& request,
                       const std::optional<TaskImage>& image) const;

  /// Has the seed peer download the task and drains the response stream,
  /// retrying a transient failure on the same seed peer. Each attempt
  /// connects to the seed peer and runs under the timeout.
  absl::Status DownloadTask(const SeedPeer& seed_peer,
                            const std::string& task_id,
                            const dfdaemon::v2::DownloadTaskRequest& request,
                            std::chrono::milliseconds timeout) const;

  const RetryPolicy retry_;
  const std::shared_ptr<SeedPeerSelector> selector_;
  const ProxyGetter getter_;
  const HttpGetter registry_getter_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_PROXY_IMPL_H_
