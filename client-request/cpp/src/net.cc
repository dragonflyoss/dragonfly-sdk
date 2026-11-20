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

#include "src/net.h"

#include <arpa/inet.h>
#include <netinet/in.h>
#include <sys/socket.h>
#include <unistd.h>

#include "absl/strings/match.h"
#include "absl/strings/str_cat.h"

namespace dragonfly::client_request::internal {
namespace {

// Returns the local ip the host reaches the remote address from. Connecting a
// datagram socket picks the route and sends nothing.
std::optional<std::string> RouteIp(int family, const sockaddr* remote,
                                   socklen_t remote_size) {
  const int fd = socket(family, SOCK_DGRAM, 0);
  if (fd < 0) {
    return std::nullopt;
  }

  std::optional<std::string> ip;
  sockaddr_storage local{};
  socklen_t local_size = sizeof(local);
  if (connect(fd, remote, remote_size) == 0 &&
      getsockname(fd, reinterpret_cast<sockaddr*>(&local), &local_size) == 0) {
    const void* address =
        family == AF_INET
            ? static_cast<const void*>(
                  &reinterpret_cast<const sockaddr_in*>(&local)->sin_addr)
            : static_cast<const void*>(
                  &reinterpret_cast<const sockaddr_in6*>(&local)->sin6_addr);
    char buffer[INET6_ADDRSTRLEN];
    if (inet_ntop(family, address, buffer, sizeof(buffer)) != nullptr) {
      ip = buffer;
    }
  }

  close(fd);
  return ip;
}

}  // namespace

std::string JoinHostPort(absl::string_view ip, int port) {
  return absl::StrContains(ip, ':') ? absl::StrCat("[", ip, "]:", port)
                                    : absl::StrCat(ip, ":", port);
}

std::optional<std::string> LocalIp() {
  sockaddr_in remote{};
  remote.sin_family = AF_INET;
  remote.sin_port = htons(80);
  inet_pton(AF_INET, "8.8.8.8", &remote.sin_addr);
  if (auto ip = RouteIp(AF_INET, reinterpret_cast<const sockaddr*>(&remote),
                        sizeof(remote))) {
    return ip;
  }

  sockaddr_in6 remote6{};
  remote6.sin6_family = AF_INET6;
  remote6.sin6_port = htons(80);
  inet_pton(AF_INET6, "2001:4860:4860::8888", &remote6.sin6_addr);
  return RouteIp(AF_INET6, reinterpret_cast<const sockaddr*>(&remote6),
                 sizeof(remote6));
}

}  // namespace dragonfly::client_request::internal
