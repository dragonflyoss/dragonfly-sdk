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

#include "dragonfly/client_request/proxy.h"

#include <set>
#include <utility>

#include "absl/memory/memory.h"
#include "absl/strings/str_cat.h"
#include "dragonfly/client_request/proxy_with_seed_peers.h"
#include "src/http_client_pool.h"
#include "src/id_generator.h"
#include "src/proxy_impl.h"
#include "src/request_internal.h"
#include "src/scheduler.h"

namespace dragonfly::client_request {
namespace internal {
namespace {

// The capacity of the pool of the clients of the seed peer proxies.
constexpr std::size_t kClientPoolCapacity = 128;

// The idle timeout of the clients in the pool.
constexpr std::chrono::minutes kClientPoolIdleTimeout(30);

// Sends the requests through the pooled clients of the seed peer proxies,
// recording any answer as a success and a refused or unreachable proxy as a
// failure, to eject the proxy that keeps failing.
class EjectingSender : public Sender {
 public:
  explicit EjectingSender(std::shared_ptr<SeedPeerSelector> selector)
      : selector_(std::move(selector)),
        client_pool_(kClientPoolCapacity, kClientPoolIdleTimeout) {}

  absl::StatusOr<std::unique_ptr<HttpResponse>> SendTo(
      const std::string& endpoint, const HttpRequest& request) override {
    auto response = SendGet(*client_pool_.Entry(endpoint), endpoint, request);
    if (response.ok() || GetErrorDetail(response.status())) {
      selector_->OnSuccess(endpoint);
    } else if (IsConnectError(response.status())) {
      selector_->OnFailure(endpoint);
    }

    return response;
  }

 private:
  const std::shared_ptr<SeedPeerSelector> selector_;
  HttpClientPool client_pool_;
};

// Returns a proxy without any seed peer, reaching the registries directly.
std::unique_ptr<ProxyImpl> NewProxyImpl(
    std::uint8_t max_retries, const std::optional<ExponentialBackoff>& backoff,
    std::chrono::milliseconds read_timeout) {
  return std::make_unique<ProxyImpl>(RetryPolicy(max_retries, backoff),
                                     read_timeout,
                                     NewRegistryGetter(HttpClient::Create()));
}

// Validates the seed peers, which the hash ring keys by name.
absl::Status ValidateSeedPeers(const std::vector<SeedPeer>& seed_peers) {
  if (seed_peers.empty()) {
    return absl::InvalidArgumentError("seed peers must not be empty");
  }

  std::set<std::string> names;
  for (const auto& seed_peer : seed_peers) {
    if (seed_peer.name.empty()) {
      return absl::InvalidArgumentError("seed peer name must not be empty");
    }

    if (!names.insert(seed_peer.name).second) {
      return absl::InvalidArgumentError(absl::StrCat(
          "seed peer name \"", seed_peer.name, "\" is duplicated"));
    }
  }

  return absl::OkStatus();
}

}  // namespace

ProxyImpl::ProxyImpl(RetryPolicy retry, std::chrono::milliseconds read_timeout,
                     HttpGetter registry_getter)
    : retry_(retry),
      selector_(std::make_shared<SeedPeerSelector>()),
      getter_(std::make_shared<EjectingSender>(selector_), retry_,
              read_timeout),
      registry_getter_(std::move(registry_getter)) {}

absl::StatusOr<GetResponse> ProxyImpl::Get(const GetRequest& request) const {
  auto endpoints = LookupEndpoints(request);
  if (!endpoints.ok()) {
    return endpoints.status();
  }

  return getter_.Get(request, std::move(*endpoints));
}

absl::StatusOr<GetResponse> ProxyImpl::GetInto(const GetRequest& request,
                                               std::ostream& output) const {
  auto endpoints = LookupEndpoints(request);
  if (!endpoints.ok()) {
    return endpoints.status();
  }

  return getter_.GetInto(request, std::move(*endpoints), output);
}

absl::StatusOr<std::vector<std::string>> ProxyImpl::LookupEndpoints(
    const GetRequest& request) const {
  if (auto status = Validate(request); !status.ok()) {
    return status;
  }

  // Generate task id for selecting seed peer.
  auto task_id = TaskId(request);
  if (!task_id.ok()) {
    return task_id.status();
  }

  auto seed_peers =
      SelectSeedPeers(*task_id, Scope::kDefault, request.replicas);
  if (!seed_peers.ok()) {
    return seed_peers.status();
  }

  // Skip the ejected proxies, failing when all are ejected.
  std::vector<std::string> endpoints;
  endpoints.reserve(seed_peers->size());
  for (const auto& seed_peer : *seed_peers) {
    std::string endpoint = SeedPeerSelector::ProxyEndpoint(seed_peer);
    if (!selector_->IsEjected(endpoint)) {
      endpoints.push_back(std::move(endpoint));
    }
  }

  if (endpoints.empty()) {
    return absl::InternalError(
        absl::StrCat("seed peers of task ", *task_id, " are all ejected"));
  }

  return endpoints;
}

absl::StatusOr<std::vector<SeedPeer>> ProxyImpl::SelectSeedPeers(
    absl::string_view task_id, Scope scope, int replicas) const {
  auto seed_peers =
      scope == Scope::kAllSeedPeers
          ? selector_->SelectAll()
          : selector_->Select(task_id, static_cast<std::uint32_t>(replicas));
  if (!seed_peers.ok()) {
    return absl::InternalError(absl::StrCat("failed to select seed peers: ",
                                            seed_peers.status().message()));
  }

  return seed_peers;
}

absl::StatusOr<ProxyImpl::ResolvedImage> ProxyImpl::ResolveImage(
    const std::string& image, const std::string& username,
    const std::string& password,
    const std::optional<std::string>& platform) const {
  auto reference = ParseImage(image);
  if (!reference.ok()) {
    return reference.status();
  }

  ResolvedImage resolved;
  resolved.reference = *reference;
  absl::Status status = retry_.Run([&] {
    auto client = RegistryClient::Create(*reference, registry_getter_, username,
                                         password);
    if (!client.ok()) {
      return client.status();
    }

    auto manifests = (*client)->ResolveManifests(platform);
    if (!manifests.ok()) {
      return manifests.status();
    }

    resolved.client = std::move(*client);
    resolved.manifests = std::move(*manifests);
    return absl::OkStatus();
  });
  if (!status.ok()) {
    return status;
  }

  return resolved;
}

}  // namespace internal

absl::StatusOr<std::unique_ptr<Proxy>> Proxy::Create(
    const std::string& scheduler_endpoint, const ProxyOptions& options) {
  if (options.scheduler_request_timeout < std::chrono::milliseconds(100)) {
    return absl::InvalidArgumentError(
        "scheduler request timeout must be at least 100 milliseconds");
  }

  if (options.health_check_interval < std::chrono::seconds(1) ||
      options.health_check_interval > std::chrono::seconds(600)) {
    return absl::InvalidArgumentError(
        "health check interval must be between 1 and 600 seconds");
  }

  if (options.read_timeout.count() <= 0) {
    return absl::InvalidArgumentError("read timeout must be greater than 0");
  }

  if (options.max_retries > 10) {
    return absl::InvalidArgumentError("max retries must be between 0 and 10");
  }

  auto target = internal::SchedulerTarget(scheduler_endpoint);
  if (!target.ok()) {
    return target.status();
  }

  auto scheduler = std::make_shared<internal::Scheduler>(
      *target, options.scheduler_request_timeout);
  auto impl = internal::NewProxyImpl(options.max_retries, options.backoff,
                                     options.read_timeout);
  if (auto status = impl->selector().Refresh(*scheduler); !status.ok()) {
    return absl::InternalError(absl::StrCat(
        "failed to create seed peer selector: ", status.message()));
  }

  // Refresh the seed peers periodically in the background.
  impl->selector().Run(std::move(scheduler), options.health_check_interval);
  return absl::WrapUnique(new Proxy(std::move(impl)));
}

Proxy::Proxy(std::unique_ptr<internal::ProxyImpl> impl)
    : impl_(std::move(impl)) {}

Proxy::~Proxy() { impl_->selector().Close(); }

absl::StatusOr<GetResponse> Proxy::Get(const GetRequest& request) {
  return impl_->Get(request);
}

absl::StatusOr<GetResponse> Proxy::GetInto(const GetRequest& request,
                                           std::ostream& output) {
  return impl_->GetInto(request, output);
}

absl::Status Proxy::Preheat(const PreheatRequest& request) {
  return impl_->Preheat(request);
}

absl::Status Proxy::PreheatImage(const PreheatImageRequest& request) {
  return impl_->PreheatImage(request);
}

absl::StatusOr<StatImageResponse> Proxy::StatImage(
    const StatImageRequest& request) {
  return impl_->StatImage(request);
}

absl::Status Proxy::Delete(const DeleteRequest& request) {
  return impl_->Delete(request);
}

absl::StatusOr<DeleteImageResponse> Proxy::DeleteImage(
    const DeleteImageRequest& request) {
  return impl_->DeleteImage(request);
}

absl::StatusOr<std::vector<std::string>> Proxy::LookupEndpoints(
    const GetRequest& request) {
  return impl_->LookupEndpoints(request);
}

absl::StatusOr<std::unique_ptr<ProxyWithSeedPeers>> ProxyWithSeedPeers::Create(
    const std::vector<SeedPeer>& seed_peers,
    const ProxyWithSeedPeersOptions& options) {
  if (auto status = internal::ValidateSeedPeers(seed_peers); !status.ok()) {
    return status;
  }

  if (options.read_timeout.count() <= 0) {
    return absl::InvalidArgumentError("read timeout must be greater than 0");
  }

  if (options.max_retries > 10) {
    return absl::InvalidArgumentError("max retries must be between 0 and 10");
  }

  auto impl = internal::NewProxyImpl(options.max_retries, options.backoff,
                                     options.read_timeout);
  impl->selector().Update(seed_peers);
  return absl::WrapUnique(new ProxyWithSeedPeers(std::move(impl)));
}

ProxyWithSeedPeers::ProxyWithSeedPeers(
    std::unique_ptr<internal::ProxyImpl> impl)
    : impl_(std::move(impl)) {}

ProxyWithSeedPeers::~ProxyWithSeedPeers() = default;

void ProxyWithSeedPeers::UpdateSeedPeers(
    const std::vector<SeedPeer>& seed_peers) {
  if (internal::ValidateSeedPeers(seed_peers).ok()) {
    impl_->selector().Update(seed_peers);
  }
}

absl::StatusOr<GetResponse> ProxyWithSeedPeers::Get(const GetRequest& request) {
  return impl_->Get(request);
}

absl::StatusOr<GetResponse> ProxyWithSeedPeers::GetInto(
    const GetRequest& request, std::ostream& output) {
  return impl_->GetInto(request, output);
}

absl::StatusOr<std::vector<std::string>> ProxyWithSeedPeers::LookupEndpoints(
    const GetRequest& request) {
  return impl_->LookupEndpoints(request);
}

}  // namespace dragonfly::client_request
