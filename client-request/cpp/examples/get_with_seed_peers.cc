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

// Lists the seed peers from the scheduler, then downloads a file via the
// Dragonfly from them and writes it to stdout.

#include <chrono>
#include <cstdlib>
#include <iostream>

#include "dragonfly/client_request/proxy.h"
#include "dragonfly/client_request/proxy_with_seed_peers.h"

using dragonfly::client_request::GetRequest;
using dragonfly::client_request::ProxyWithSeedPeers;
using dragonfly::client_request::SchedulerClient;

int main(int argc, char* argv[]) {
  if (argc != 3) {
    std::cerr << "usage: " << argv[0] << " <scheduler-endpoint> <url>\n";
    return EXIT_FAILURE;
  }

  auto scheduler_client =
      SchedulerClient::Create(argv[1], std::chrono::seconds(5));
  if (!scheduler_client.ok()) {
    std::cerr << scheduler_client.status() << "\n";
    return EXIT_FAILURE;
  }

  auto seed_peers = (*scheduler_client)->ListSeedPeers();
  if (!seed_peers.ok()) {
    std::cerr << seed_peers.status() << "\n";
    return EXIT_FAILURE;
  }

  for (const auto& seed_peer : *seed_peers) {
    std::cerr << "seed peer " << seed_peer.hostname << " " << seed_peer.ip
              << ":" << seed_peer.proxy_port << " healthy: " << std::boolalpha
              << seed_peer.healthy << "\n";
  }

  auto proxy = ProxyWithSeedPeers::Create(*seed_peers);
  if (!proxy.ok()) {
    std::cerr << proxy.status() << "\n";
    return EXIT_FAILURE;
  }

  GetRequest request;
  request.url = argv[2];
  auto response = (*proxy)->GetInto(request, std::cout);
  if (!response.ok()) {
    std::cerr << response.status() << "\n";
    return EXIT_FAILURE;
  }

  return EXIT_SUCCESS;
}
