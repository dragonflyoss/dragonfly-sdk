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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_REQUEST_INTERNAL_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_REQUEST_INTERNAL_H_

#include <string>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/string_view.h"
#include "dragonfly/client_request/request.h"

namespace dragonfly::client_request::internal {

/// Validates the request: positive replicas and an http or https url with a
/// host.
absl::Status Validate(const GetRequest& request);

/// Validates the request: positive replicas.
absl::Status Validate(const PreheatRequest& request);

/// Validates the request: positive replicas and concurrent task count, an
/// "os/arch" platform.
absl::Status Validate(const PreheatImageRequest& request);

/// Validates the request: a positive concurrent peer count, an "os/arch"
/// platform.
absl::Status Validate(const StatImageRequest& request);

/// Validates the request: positive replicas.
absl::Status Validate(const DeleteRequest& request);

/// Validates the request: a positive concurrent peer count, an "os/arch"
/// platform.
absl::Status Validate(const DeleteImageRequest& request);

/// Sets the header, replacing its values whatever the case of their name.
void SetHeader(Header* header, absl::string_view name, absl::string_view value);

/// Returns the headers of the request with the P2P related ones applied.
absl::StatusOr<Header> ProxyHeaders(const GetRequest& request);

/// Normalizes a platform to "os/arch" as containerd does, such as
/// "Linux/x86_64" to "linux/amd64", failing when it is not "os/arch".
absl::StatusOr<std::string> NormalizePlatform(absl::string_view platform);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_REQUEST_INTERNAL_H_
