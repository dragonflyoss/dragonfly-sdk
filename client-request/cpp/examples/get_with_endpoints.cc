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

// Looks up the endpoints of the seed peers serving the url via the Dragonfly,
// then downloads the file from a randomly picked endpoint and writes it to
// stdout.

#include <cstdlib>
#include <iostream>

#include "dragonfly/client_request/proxy.h"
#include "dragonfly/client_request/proxy_with_endpoints.h"

using dragonfly::client_request::GetRequest;
using dragonfly::client_request::Proxy;
using dragonfly::client_request::ProxyWithEndpoints;

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

  GetRequest request;
  request.url = argv[2];
  auto endpoints = (*proxy)->LookupEndpoints(request);
  if (!endpoints.ok()) {
    std::cerr << endpoints.status() << "\n";
    return EXIT_FAILURE;
  }

  auto proxy_with_endpoints = ProxyWithEndpoints::Create(*endpoints);
  if (!proxy_with_endpoints.ok()) {
    std::cerr << proxy_with_endpoints.status() << "\n";
    return EXIT_FAILURE;
  }

  auto response = (*proxy_with_endpoints)->GetInto(request, std::cout);
  if (!response.ok()) {
    std::cerr << response.status() << "\n";
    return EXIT_FAILURE;
  }

  return EXIT_SUCCESS;
}
