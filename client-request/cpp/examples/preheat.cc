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

// Preheats a file or an OCI image to the seed peers via the Dragonfly, to the
// replicas of the task by default or to all seed peers with the
// all_seed_peers scope.

#include <cstdlib>
#include <iostream>
#include <string>

#include "dragonfly/client_request/proxy.h"

using dragonfly::client_request::PreheatImageRequest;
using dragonfly::client_request::PreheatRequest;
using dragonfly::client_request::Proxy;
using dragonfly::client_request::Scope;

int main(int argc, char* argv[]) {
  if (argc != 4 && argc != 5) {
    std::cerr << "usage: " << argv[0]
              << " <scheduler-endpoint> file|image <url-or-image>"
                 " [default|all_seed_peers]\n";
    return EXIT_FAILURE;
  }

  std::string kind = argv[2];
  Scope scope = argc == 5 && std::string(argv[4]) == "all_seed_peers"
                    ? Scope::kAllSeedPeers
                    : Scope::kDefault;

  auto proxy = Proxy::Create(argv[1]);
  if (!proxy.ok()) {
    std::cerr << proxy.status() << "\n";
    return EXIT_FAILURE;
  }

  absl::Status status;
  if (kind == "file") {
    PreheatRequest request;
    request.url = argv[3];
    request.scope = scope;
    status = (*proxy)->Preheat(request);
  } else {
    PreheatImageRequest request;
    request.image = argv[3];
    request.scope = scope;
    status = (*proxy)->PreheatImage(request);
  }

  if (!status.ok()) {
    std::cerr << status << "\n";
    return EXIT_FAILURE;
  }

  return EXIT_SUCCESS;
}
