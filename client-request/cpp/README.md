# client-request (C++)

[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client. It sends requests to remote servers
via the Dragonfly P2P network, supporting streaming and buffered GET requests,
preheating files or OCI images through seed peers and deleting preheated files
or OCI images from them.

It is the C++ implementation of the Rust crate
[dragonfly-client-request](../rust) and generates identical task ids and seed
peer selections, pinned by shared cross-language test vectors.

## Install

The library needs a C++17 compiler, CMake 3.24 or later, and gRPC, Protobuf,
Abseil, RE2, libcurl and OpenSSL. RE2 is installed with gRPC. The protos of the
Dragonfly API and [ada](https://github.com/ada-url/ada), the WHATWG url parser,
are fetched when the project is configured:

```console
# Ubuntu
sudo apt-get install -y cmake ninja-build g++ libgrpc++-dev libprotobuf-dev \
    protobuf-compiler protobuf-compiler-grpc libcurl4-openssl-dev libssl-dev libabsl-dev

# macOS
brew install cmake ninja grpc
```

Add it to a CMake project with `FetchContent`:

```cmake
include(FetchContent)
FetchContent_Declare(
  dragonfly_sdk
  GIT_REPOSITORY https://github.com/dragonflyoss/dragonfly-sdk.git
  GIT_TAG main
  SOURCE_SUBDIR client-request/cpp)
FetchContent_MakeAvailable(dragonfly_sdk)

target_link_libraries(app PRIVATE dragonfly::client_request)
```

Or install it and find the package:

```console
cmake -S client-request/cpp -B build -G Ninja -DCMAKE_BUILD_TYPE=Release -DBUILD_TESTING=OFF
cmake --build build
cmake --install build --prefix /usr/local
```

```cmake
find_package(dragonfly-client-request CONFIG REQUIRED)
target_link_libraries(app PRIVATE dragonfly::client_request)
```

## Usage

```cpp
#include <iostream>

#include "dragonfly/client_request/proxy.h"

using dragonfly::client_request::GetRequest;
using dragonfly::client_request::Proxy;

int main() {
  auto proxy = Proxy::Create("http://127.0.0.1:8002");
  if (!proxy.ok()) {
    std::cerr << proxy.status() << "\n";
    return 1;
  }

  GetRequest request;
  request.url = "https://example.com/file.txt";
  auto response = (*proxy)->Get(request);
  if (!response.ok()) {
    std::cerr << response.status() << "\n";
    return 1;
  }

  // Read response->body...
}
```

Optional request parameters are fields of the request:

```cpp
GetRequest request;
request.url = "https://example.com/file.txt";
request.tag = "tag";
request.application = "app";
request.timeout = std::chrono::seconds(30);
```

Every call returns an `absl::Status` or an `absl::StatusOr`, and nothing
throws. An invalid argument is `kInvalidArgument`, a timeout is
`kDeadlineExceeded` and a connect or transport error is `kInternal`. A non-2xx
answer of a seed peer proxy, a backend or a dfdaemon carries its status code,
headers and message:

```cpp
#include "dragonfly/client_request/errors.h"

auto response = (*proxy)->Get(request);
if (!response.ok()) {
  if (auto detail = dragonfly::client_request::GetErrorDetail(response.status())) {
    std::cerr << "status code: " << detail->status_code << ", message: " << detail->message << "\n";
  }
}
```

Retry transient failures. A request is retried on another seed peer when the
peer cannot be reached or answers too slowly, or the proxy, backend or dfdaemon
answers `5xx`, `408` or `429`, since another seed peer may not be rate limited
or out of space. Any other answer, such as `404` or a dfdaemon `422`, is
definitive and returns at once. A body that breaks off is retried too: `Get`
and `GetInto` send the request again within the same retries, its `Range`
narrowed to the bytes not read yet, so the output of `GetInto` is never
rewound. That needs the response to tell its length, by a `Content-Length` or
the `Content-Range` of a single range; a body without them, such as a chunked
one, fails when it breaks off. Preheating retries the same way on each replica
seed peer.

The default is one retry at once and `max_retries = 0` disables retries. An
exponential backoff spreads the retries out. The read timeout of the proxy
bounds the wait for the response and for every read of the body. The request
timeout applies to each attempt:

```cpp
using dragonfly::client_request::ExponentialBackoff;
using dragonfly::client_request::ProxyOptions;

ProxyOptions options;
options.max_retries = 3;
options.backoff = ExponentialBackoff{};
auto proxy = Proxy::Create("http://127.0.0.1:8002", options);
```

Preheat a file or an OCI image to the seed peers. An image is preheated for all
its platforms unless a platform is set:

```cpp
PreheatRequest preheat_request;
preheat_request.url = "https://example.com/file.txt";
absl::Status status = (*proxy)->Preheat(preheat_request);

PreheatImageRequest preheat_image_request;
preheat_image_request.image = "docker.io/library/nginx:latest";
status = (*proxy)->PreheatImage(preheat_image_request);
```

Query the distribution of an OCI image, listing the layers cached by each
seed peer:

```cpp
StatImageRequest stat_image_request;
stat_image_request.image = "docker.io/library/nginx:latest";
auto response = (*proxy)->StatImage(stat_image_request);
if (response.ok()) {
  std::cout << "image has " << response->layers.size() << " layers\n";
  for (const auto& peer : response->peers) {
    std::cout << "peer " << peer.hostname << " (" << peer.ip << ") cached "
              << peer.cached_layers.size() << " layers\n";
  }
}
```

Delete a preheated file or an OCI image from the seed peers. A file delete
should carry the parameters the preheat used, such as the replicas and the
scope, so it addresses the same task and seed peers, and a seed peer answering
`NotFound` counts as deleted. An image is deleted from all healthy seed peers,
for all its platforms unless a platform is set, and a task still referenced by
another image or platform is retained:

```cpp
DeleteRequest delete_request;
delete_request.url = "https://example.com/file.txt";
absl::Status status = (*proxy)->Delete(delete_request);

DeleteImageRequest delete_image_request;
delete_image_request.image = "docker.io/library/nginx:latest";
auto response = (*proxy)->DeleteImage(delete_image_request);
if (response.ok()) {
  for (const auto& peer : response->peers) {
    std::cout << "peer " << peer.hostname << " (" << peer.ip << ") deleted "
              << peer.deleted_task_ids.size() << " tasks and retained "
              << peer.retained_task_ids.size() << " tasks\n";
  }
}
```

Preheat with multiple replicas and scatter downloads across them. Preheating
writes the file to the given number of distinct seed peers, and downloading
scatters each request across those replicas by picking a random one, retrying
on the others up to the max retries. Preheating fails when fewer of the task's
replicas are healthy than requested, while downloading uses its healthy
replicas whose proxies are not ejected, failing when none is. The default
replicas is 2:

```cpp
PreheatRequest preheat_request;
preheat_request.url = "https://example.com/file.txt";
preheat_request.replicas = 3;
absl::Status status = (*proxy)->Preheat(preheat_request);

GetRequest request;
request.url = "https://example.com/file.txt";
request.replicas = 3;
auto response = (*proxy)->Get(request);
```

Preheat to all seed peers regardless of the replicas with the all seed peers
scope, aligned with the `all_seed_peers` scope of the manager preheat job, so
any seed peer a download picks holds the file. Delete with the same scope to
remove the file from every seed peer:

```cpp
PreheatRequest preheat_request;
preheat_request.url = "https://example.com/file.txt";
preheat_request.scope = Scope::kAllSeedPeers;
absl::Status status = (*proxy)->Preheat(preheat_request);

DeleteRequest delete_request;
delete_request.url = "https://example.com/file.txt";
delete_request.scope = Scope::kAllSeedPeers;
status = (*proxy)->Delete(delete_request);
```

Look up the endpoints of the seed peers serving a request, then create a proxy
bound to those endpoints and download from them directly, scattering the
request across them. The endpoints proxy keeps a client with a reusable
connection pool per endpoint and doesn't sync seed peers from the scheduler:

```cpp
#include "dragonfly/client_request/proxy_with_endpoints.h"

GetRequest request;
request.url = "https://example.com/file.txt";
auto endpoints = (*proxy)->LookupEndpoints(request);

auto proxy_with_endpoints = ProxyWithEndpoints::Create(*endpoints);
auto response = (*proxy_with_endpoints)->Get(request);

// Or write the response body directly into an output stream:
// auto response = (*proxy_with_endpoints)->GetInto(request, std::cout);
```

Share the seed peers across processes, such as several clients on a host, so
only one of them lists the seed peers from the scheduler and checks their
health. `SchedulerClient::ListSeedPeers` returns every seed peer with its
health, and a proxy created with `ProxyWithSeedPeers::Create` neither connects
to the scheduler nor refreshes them, which `UpdateSeedPeers` does instead. The
consistent hash ring is built from every seed peer, healthy or not, so a task
stays on its replicas: an unhealthy replica is skipped, the request failing
when every replica is, and serves the task again once it recovers. A seed peer
proxy refusing the connection or unreachable 5 times in a row is skipped for
30 seconds:

```cpp
#include "dragonfly/client_request/proxy_with_seed_peers.h"

auto scheduler_client = SchedulerClient::Create("http://127.0.0.1:8002", std::chrono::seconds(5));
auto seed_peers = (*scheduler_client)->ListSeedPeers();

auto proxy = ProxyWithSeedPeers::Create(*seed_peers);

// Later, replace them with the ones listed again, such as by another process.
seed_peers = (*scheduler_client)->ListSeedPeers();
(*proxy)->UpdateSeedPeers(*seed_peers);
```

See [examples](./examples) for runnable examples.

## Development

```console
cmake -S . -B build -G Ninja -DCMAKE_COMPILE_WARNING_AS_ERROR=ON
cmake --build build
ctest --test-dir build --output-on-failure
```

The code follows the [Google C++ Style Guide](https://google.github.io/styleguide/cppguide.html),
formatted by `clang-format` and checked by `clang-tidy` with the configurations
in this directory.

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
