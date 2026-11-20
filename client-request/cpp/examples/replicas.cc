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

// Preheats the url to three replicas of seed peers via the Dragonfly, then
// downloads it with the request scattered across the replicas, writing the
// content to stdout. The default replicas is 2 when not set.

#include <cstdlib>
#include <iostream>

#include "dragonfly/client_request/proxy.h"

using dragonfly::client_request::GetRequest;
using dragonfly::client_request::PreheatRequest;
using dragonfly::client_request::Proxy;

int main(int argc, char* argv[]) {
  if (argc != 3) {
    std::cerr << "usage: " << argv[0] << " <scheduler-endpoint> <url>\n";
    return EXIT_FAILURE;
  }

  auto proxy = Proxy::Create(argv[1]);
  if (!proxy.ok()) {
    std::cerr << proxy.status() << "\n";
    return EXIT_FAILURE;
  }

  PreheatRequest preheat_request;
  preheat_request.url = argv[2];
  preheat_request.replicas = 3;
  absl::Status status = (*proxy)->Preheat(preheat_request);
  if (!status.ok()) {
    std::cerr << status << "\n";
    return EXIT_FAILURE;
  }

  GetRequest request;
  request.url = argv[2];
  request.replicas = 3;
  auto response = (*proxy)->GetInto(request, std::cout);
  if (!response.ok()) {
    std::cerr << response.status() << "\n";
    return EXIT_FAILURE;
  }

  return EXIT_SUCCESS;
}
