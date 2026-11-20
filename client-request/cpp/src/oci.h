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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_OCI_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_OCI_H_

#include <functional>
#include <map>
#include <memory>
#include <mutex>
#include <optional>
#include <string>
#include <vector>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"
#include "absl/time/time.h"
#include "dragonfly/client_request/errors.h"
#include "src/http_client.h"

namespace dragonfly::client_request::internal {

/// The registry coordinates of an image.
struct Reference {
  /// The scheme of the registry, "https".
  std::string scheme;

  /// The registry host, "registry-1.docker.io" for docker.io.
  std::string registry;

  /// The repository name.
  std::string repository;

  /// The tag or the digest.
  std::string reference;

  /// Returns <scheme>://<registry>/v2/<repository>/manifests/<reference>.
  std::string ManifestUrl() const;

  /// Returns <scheme>://<registry>/v2/<repository>/blobs/<digest>.
  std::string BlobUrl(absl::string_view digest) const;

  /// Returns registry/repository:tag or registry/repository@digest.
  std::string ToString() const;
};

/// Parses an image reference with the docker conventions, so "nginx" is
/// "docker.io/library/nginx:latest".
absl::StatusOr<Reference> ParseImage(absl::string_view image);

/// The answer of a registry to a GET request.
struct HttpResult {
  /// The HTTP status code.
  int status_code = 0;

  /// The HTTP headers, names lowercased.
  Header header;

  /// The body.
  std::string body;
};

/// Sends a GET request to a registry. A transport failure is an error; any
/// answer, 2xx or not, is a result.
using HttpGetter = std::function<absl::StatusOr<HttpResult>(
    const std::string& url, const Header& header)>;

/// A manifest resolved from an image.
struct Manifest {
  /// The manifest url referenced by the digest of the manifest.
  std::string url;

  /// The platform as "os/arch", empty for a single-platform image resolved
  /// for all platforms.
  std::string platform;

  /// The blob urls of the config and the layers, each once.
  std::vector<std::string> blob_urls;
};

/// Returns the getter that sends the requests of a registry through the
/// client.
HttpGetter NewRegistryGetter(std::shared_ptr<HttpClient> client);

/// The media types a manifest request accepts.
const std::vector<std::string>& ManifestMediaTypes();

/// The client of the registry of an image: negotiates its v2 authentication
/// and resolves the manifests of the image. It is thread-safe.
class RegistryClient {
 public:
  /// Pings the registry of the reference for its authentication challenge.
  /// Anonymous when the username or the password is empty.
  static absl::StatusOr<std::unique_ptr<RegistryClient>> Create(
      Reference reference, HttpGetter getter, std::string username,
      std::string password);

  /// Returns the Authorization header value of the registry: the bearer token,
  /// kept and renewed when it expires, or the basic credentials, or an empty
  /// string when the registry needs none.
  absl::StatusOr<std::string> Authorization();

  /// Resolves the manifests of the image, an image index included: all the
  /// platforms when the platform is unset, otherwise only the given "os/arch".
  /// Fails when no manifest matches.
  absl::StatusOr<std::vector<Manifest>> ResolveManifests(
      const std::optional<std::string>& platform);

 private:
  RegistryClient(Reference reference, HttpGetter getter, std::string username,
                 std::string password);

  /// Fetches a token from the realm of the bearer challenge and keeps it.
  absl::Status FetchToken();

  /// Resolves the manifests of the reference, the entries of an image index
  /// included.
  absl::StatusOr<std::vector<Manifest>> Resolve(
      const Reference& reference, const std::optional<std::string>& platform);

  Reference reference_;
  HttpGetter getter_;
  std::string username_;
  std::string password_;

  /// The parameters of the bearer challenge of the registry, if any.
  std::optional<std::map<std::string, std::string>> bearer_;

  /// Whether the registry asks for the basic credentials.
  bool basic_ = false;

  /// Guards the token and its expiration.
  std::mutex mu_;
  std::string token_;
  absl::Time token_expiration_;
};

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_OCI_H_
