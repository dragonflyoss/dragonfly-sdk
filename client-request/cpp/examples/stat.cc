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

// Queries the distribution of an OCI image in the seed peers via the
// Dragonfly.

#include <algorithm>
#include <cstdlib>
#include <iostream>

#include "dragonfly/client_request/proxy.h"

using dragonfly::client_request::Layer;
using dragonfly::client_request::Proxy;
using dragonfly::client_request::StatImageRequest;

int main(int argc, char* argv[]) {
  if (argc != 3) {
    std::cerr << "usage: " << argv[0] << " <scheduler-endpoint> <image>\n";
    return EXIT_FAILURE;
  }

  auto proxy = Proxy::Create(argv[1]);
  if (!proxy.ok()) {
    std::cerr << proxy.status() << "\n";
    return EXIT_FAILURE;
  }

  StatImageRequest request;
  request.image = argv[2];
  auto response = (*proxy)->StatImage(request);
  if (!response.ok()) {
    std::cerr << response.status() << "\n";
    return EXIT_FAILURE;
  }

  std::cout << "image has " << response->layers.size() << " layers\n";
  for (const auto& peer : response->peers) {
    auto finished =
        std::count_if(peer.cached_layers.begin(), peer.cached_layers.end(),
                      [](const Layer& layer) { return layer.is_finished; });
    std::cout << "peer " << peer.hostname << " (" << peer.ip << ") finished "
              << finished << " of " << peer.cached_layers.size()
              << " cached layers\n";
  }

  return EXIT_SUCCESS;
}
