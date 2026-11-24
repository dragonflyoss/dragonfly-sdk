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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_NET_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_NET_H_

#include <optional>
#include <string>

#include "absl/strings/string_view.h"

namespace dragonfly::client_request::internal {

/// Returns the socket address of an ip and a port, an IPv6 ip in brackets.
std::string JoinHostPort(absl::string_view ip, int port);

/// Returns the local ip of the host, IPv4 preferred, nullopt when none is
/// found.
std::optional<std::string> LocalIp();

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_NET_H_
