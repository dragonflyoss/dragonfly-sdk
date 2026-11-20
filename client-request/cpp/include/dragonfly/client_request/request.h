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

#ifndef DRAGONFLY_CLIENT_REQUEST_REQUEST_H_
#define DRAGONFLY_CLIENT_REQUEST_REQUEST_H_

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <optional>
#include <ostream>
#include <string>
#include <vector>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "dragonfly/client_request/errors.h"

namespace dragonfly::client_request {

/// Returns the default filtered query params to generate the task id: the
/// signature params of S3, GCS, OSS, OBS, COS and containerd.
const std::vector<std::string>& DefaultFilteredQueryParams();

/// The seed peers a preheat or a delete addresses.
enum class Scope {
  /// The replicas of the task, in the consistent hash ring order of its id.
  kDefault,
  /// All seed peers regardless of the replicas.
  kAllSeedPeers,
};

/// The exponential delay between the retries of a request.
struct ExponentialBackoff {
  /// The delay before the first retry.
  std::chrono::milliseconds initial_interval{500};

  /// The factor the delay grows by on every retry.
  double multiplier = 1.5;

  /// The random share of the delay added to or taken from it.
  double randomization_factor = 0.5;

  /// The longest delay.
  std::chrono::milliseconds max_interval{60'000};
};

/// A GET request sent via the Dragonfly.
struct GetRequest {
  /// The url of the request, http or https.
  std::string url;

  /// The headers of the request.
  Header header;

  /// The task piece length.
  std::optional<std::uint64_t> piece_length;

  /// The tag that identifies different tasks for the same url.
  std::string tag;

  /// The application that identifies different tasks for the same url.
  std::string application;

  /// The query params left out of the task id, so urls differing only in
  /// them, such as signatures, share a task.
  std::vector<std::string> filtered_query_params = DefaultFilteredQueryParams();

  /// The content the task id is calculated from instead of the url.
  std::string content_for_calculating_task_id;

  /// Whether the task id of an OCI blob url is its digest, so the blob is
  /// shared across registries.
  bool enable_task_id_based_blob_digest = true;

  /// The task priority, refer to
  /// https://github.com/dragonflyoss/api/blob/main/proto/common.proto.
  std::optional<std::int32_t> priority;

  /// The number of seed peers serving the task.
  int replicas = 2;

  /// The timeout of each attempt of the request.
  std::chrono::milliseconds timeout = std::chrono::minutes(10);
};

/// The streaming body of a response. Destroying it releases the response.
class Body {
 public:
  virtual ~Body() = default;

  /// Reads up to `size` bytes into `buffer` and returns the bytes read, 0 at
  /// the end of the body. A body that breaks off is sent again on the next
  /// seed peer for the bytes left.
  virtual absl::StatusOr<std::size_t> Read(char* buffer, std::size_t size) = 0;
};

/// A GET response received via the Dragonfly.
struct GetResponse {
  /// Whether the response is successful.
  bool success = false;

  /// The status code of the response.
  int status_code = 0;

  /// The headers of the response.
  Header header;

  /// The content of the response, null for GetInto.
  std::unique_ptr<Body> body;
};

/// A request to preheat a file: the seed peers download it without streaming
/// it back.
struct PreheatRequest {
  /// The url of the file, http or https.
  std::string url;

  /// The headers of the download.
  Header header;

  /// The task piece length.
  std::optional<std::uint64_t> piece_length;

  /// The tag that identifies different tasks for the same url.
  std::string tag;

  /// The application that identifies different tasks for the same url.
  std::string application;

  /// The query params left out of the task id.
  std::vector<std::string> filtered_query_params = DefaultFilteredQueryParams();

  /// The content the task id is calculated from instead of the url.
  std::string content_for_calculating_task_id;

  /// Whether the task id of an OCI blob url is its digest.
  bool enable_task_id_based_blob_digest = true;

  /// The task priority.
  std::optional<std::int32_t> priority;

  /// The number of seed peers preheating the task.
  int replicas = 2;

  /// The seed peers the preheat addresses.
  Scope scope = Scope::kDefault;

  /// The timeout of each attempt on a seed peer.
  std::chrono::milliseconds timeout = std::chrono::minutes(10);
};

/// A request to preheat an OCI image: the seed peers download its manifests
/// and blobs.
struct PreheatImageRequest {
  /// The image reference, such as "ghcr.io/dragonflyoss/client:v1.0.0".
  std::string image;

  /// The username of the registry, anonymous when empty.
  std::string username;

  /// The password of the registry.
  std::string password;

  /// The platform as "os/arch", all the platforms when unset.
  std::optional<std::string> platform;

  /// The task piece length.
  std::optional<std::uint64_t> piece_length;

  /// The tag that identifies different tasks for the same url.
  std::string tag;

  /// The application that identifies different tasks for the same url.
  std::string application;

  /// The query params left out of the task id.
  std::vector<std::string> filtered_query_params = DefaultFilteredQueryParams();

  /// Whether the task id of an OCI blob url is its digest.
  bool enable_task_id_based_blob_digest = true;

  /// The task priority.
  std::optional<std::int32_t> priority;

  /// The number of seed peers preheating each manifest and blob.
  int replicas = 2;

  /// The seed peers the preheat addresses.
  Scope scope = Scope::kDefault;

  /// The timeout of each attempt on a seed peer.
  std::chrono::milliseconds timeout = std::chrono::minutes(10);

  /// The number of manifests and blobs preheated concurrently.
  int concurrent_task_count = 4;
};

/// A request to stat an OCI image: which seed peers hold which layers.
struct StatImageRequest {
  /// The image reference.
  std::string image;

  /// The username of the registry, anonymous when empty.
  std::string username;

  /// The password of the registry.
  std::string password;

  /// The platform as "os/arch", all the platforms when unset.
  std::optional<std::string> platform;

  /// The task piece length.
  std::optional<std::uint64_t> piece_length;

  /// The tag that identifies different tasks for the same url.
  std::string tag;

  /// The application that identifies different tasks for the same url.
  std::string application;

  /// The query params left out of the task id.
  std::vector<std::string> filtered_query_params = DefaultFilteredQueryParams();

  /// Whether the task id of an OCI blob url is its digest.
  bool enable_task_id_based_blob_digest = true;

  /// The timeout of each stat on a seed peer.
  std::chrono::milliseconds timeout = std::chrono::minutes(10);

  /// The number of seed peers addressed concurrently.
  int concurrent_peer_count = 500;
};

/// A layer of an image cached by a seed peer.
struct Layer {
  /// The blob url of the layer.
  std::string url;

  /// Whether the seed peer finished downloading the layer.
  bool is_finished = false;
};

/// The layers of an image a seed peer has cached.
struct PeerStatedImage {
  /// The ip of the seed peer.
  std::string ip;

  /// The hostname of the seed peer.
  std::string hostname;

  /// The layers the seed peer has cached.
  std::vector<Layer> cached_layers;
};

/// The distribution of an image across the seed peers.
struct StatImageResponse {
  /// The blob urls of the image, config and layers.
  std::vector<std::string> layers;

  /// The seed peers that cached at least one layer.
  std::vector<PeerStatedImage> peers;
};

/// A request to delete a preheated file from the seed peers.
struct DeleteRequest {
  /// The url of the file, http or https.
  std::string url;

  /// The task piece length.
  std::optional<std::uint64_t> piece_length;

  /// The tag that identifies different tasks for the same url.
  std::string tag;

  /// The application that identifies different tasks for the same url.
  std::string application;

  /// The query params left out of the task id.
  std::vector<std::string> filtered_query_params = DefaultFilteredQueryParams();

  /// The content the task id is calculated from instead of the url.
  std::string content_for_calculating_task_id;

  /// Whether the task id of an OCI blob url is its digest.
  bool enable_task_id_based_blob_digest = true;

  /// The number of seed peers holding the task.
  int replicas = 2;

  /// The seed peers the delete addresses.
  Scope scope = Scope::kDefault;

  /// The timeout of each attempt on a seed peer.
  std::chrono::milliseconds timeout = std::chrono::minutes(10);
};

/// A request to delete a preheated OCI image from every healthy seed peer.
struct DeleteImageRequest {
  /// The image reference.
  std::string image;

  /// The platform as "os/arch", all the platforms when unset.
  std::optional<std::string> platform;

  /// The timeout of each attempt on a seed peer.
  std::chrono::milliseconds timeout = std::chrono::minutes(10);

  /// The number of seed peers addressed concurrently.
  int concurrent_peer_count = 500;
};

/// The tasks of an image a seed peer deleted and retained.
struct PeerDeletedImage {
  /// The ip of the seed peer.
  std::string ip;

  /// The hostname of the seed peer.
  std::string hostname;

  /// The ids of the tasks deleted.
  std::vector<std::string> deleted_task_ids;

  /// The ids of the tasks retained, still referenced by another image or
  /// platform.
  std::vector<std::string> retained_task_ids;
};

/// The seed peers that deleted an image.
struct DeleteImageResponse {
  /// The seed peers that answered, a seed peer that fails is left out.
  std::vector<PeerDeletedImage> peers;
};

/// The interface for sending requests via the Dragonfly, so a client is
/// replaced by a mock in tests. Proxy implements it.
class Request {
 public:
  virtual ~Request() = default;

  /// Sends a GET request via the Dragonfly and returns a response with a
  /// streaming body.
  virtual absl::StatusOr<GetResponse> Get(const GetRequest& request) = 0;

  /// Sends a GET request via the Dragonfly and writes the response body into
  /// the output, which is never rewound.
  virtual absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                              std::ostream& output) = 0;

  /// Preheats a file: has every replica seed peer, or every seed peer with
  /// Scope::kAllSeedPeers, download it. A transient failure is retried on the
  /// same seed peer.
  virtual absl::Status Preheat(const PreheatRequest& request) = 0;

  /// Preheats an OCI image: resolves its manifests, all the platforms unless
  /// a platform is set, and has the seed peers download every manifest and
  /// blob.
  virtual absl::Status PreheatImage(const PreheatImageRequest& request) = 0;

  /// Reports which healthy seed peers hold which layers of an OCI image.
  virtual absl::StatusOr<StatImageResponse> StatImage(
      const StatImageRequest& request) = 0;

  /// Deletes a preheated file from every replica seed peer, or every seed
  /// peer with Scope::kAllSeedPeers. A seed peer without the task counts as
  /// deleted.
  virtual absl::Status Delete(const DeleteRequest& request) = 0;

  /// Deletes a preheated OCI image from every healthy seed peer, retaining
  /// the tasks other images or platforms reference.
  virtual absl::StatusOr<DeleteImageResponse> DeleteImage(
      const DeleteImageRequest& request) = 0;

  /// Returns the proxy endpoints, such as "http://127.0.0.1:4001", of the
  /// seed peers that would serve the request, in the consistent hash ring
  /// order, up to the replicas and skipping the unhealthy or ejected ones.
  virtual absl::StatusOr<std::vector<std::string>> LookupEndpoints(
      const GetRequest& request) = 0;
};

/// The interface for sending requests via fixed seed peer endpoints.
/// ProxyWithEndpoints implements it.
class RequestWithEndpoints {
 public:
  virtual ~RequestWithEndpoints() = default;

  /// Sends a GET request to a randomly picked endpoint and returns a response
  /// with a streaming body. A transient failure is retried on the others.
  virtual absl::StatusOr<GetResponse> Get(const GetRequest& request) = 0;

  /// Sends a GET request to a randomly picked endpoint and writes the
  /// response body into the output, which is never rewound.
  virtual absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                              std::ostream& output) = 0;
};

/// The interface for sending requests via the seed peers given at
/// construction. ProxyWithSeedPeers implements it.
class RequestWithSeedPeers {
 public:
  virtual ~RequestWithSeedPeers() = default;

  /// Sends a GET request via the seed peers, see Request::Get.
  virtual absl::StatusOr<GetResponse> Get(const GetRequest& request) = 0;

  /// Sends a GET request via the seed peers, see Request::GetInto.
  virtual absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                              std::ostream& output) = 0;

  /// Returns the proxy endpoints of the seed peers that would serve the
  /// request, see Request::LookupEndpoints.
  virtual absl::StatusOr<std::vector<std::string>> LookupEndpoints(
      const GetRequest& request) = 0;
};

}  // namespace dragonfly::client_request

#endif  // DRAGONFLY_CLIENT_REQUEST_REQUEST_H_
