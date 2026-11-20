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

// Deletes a preheated file or an OCI image from the seed peers via the
// Dragonfly. A file is deleted from the replicas of the task by default or
// from all seed peers with the all_seed_peers scope, an image is deleted from
// all healthy seed peers.

#include <cstdlib>
#include <iostream>
#include <string>

#include "dragonfly/client_request/proxy.h"

using dragonfly::client_request::DeleteImageRequest;
using dragonfly::client_request::DeleteRequest;
using dragonfly::client_request::Proxy;
using dragonfly::client_request::Scope;

int main(int argc, char* argv[]) {
  if (argc != 4 && argc != 5) {
    std::cerr << "usage: " << argv[0]
              << " <scheduler-endpoint> file|image <url-or-image>"
                 " [default|all_seed_peers]\n";
    return EXIT_FAILURE;
  }

  auto proxy = Proxy::Create(argv[1]);
  if (!proxy.ok()) {
    std::cerr << proxy.status() << "\n";
    return EXIT_FAILURE;
  }

  if (std::string(argv[2]) == "file") {
    DeleteRequest request;
    request.url = argv[3];
    request.scope = argc == 5 && std::string(argv[4]) == "all_seed_peers"
                        ? Scope::kAllSeedPeers
                        : Scope::kDefault;
    absl::Status status = (*proxy)->Delete(request);
    if (!status.ok()) {
      std::cerr << status << "\n";
      return EXIT_FAILURE;
    }

    return EXIT_SUCCESS;
  }

  DeleteImageRequest request;
  request.image = argv[3];
  auto response = (*proxy)->DeleteImage(request);
  if (!response.ok()) {
    std::cerr << response.status() << "\n";
    return EXIT_FAILURE;
  }

  for (const auto& peer : response->peers) {
    std::cout << "peer " << peer.hostname << " (" << peer.ip << ") deleted "
              << peer.deleted_task_ids.size() << " tasks and retained "
              << peer.retained_task_ids.size() << " tasks\n";
  }

  return EXIT_SUCCESS;
}
