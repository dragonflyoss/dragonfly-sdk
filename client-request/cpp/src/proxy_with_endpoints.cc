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

#include "dragonfly/client_request/proxy_with_endpoints.h"

#include <map>
#include <utility>

#include "absl/memory/memory.h"
#include "absl/strings/str_cat.h"
#include "src/get.h"
#include "src/request_internal.h"
#include "src/url.h"

namespace dragonfly::client_request {
namespace internal {
namespace {

// Sends the requests through the clients of the fixed endpoints, each
// endpoint with a client of its own, so it reuses its connections.
class EndpointsSender : public Sender {
 public:
  explicit EndpointsSender(const std::vector<std::string>& endpoints) {
    for (const auto& endpoint : endpoints) {
      clients_.emplace(endpoint, HttpClient::Create());
    }
  }

  absl::StatusOr<std::unique_ptr<HttpResponse>> SendTo(
      const std::string& endpoint, const HttpRequest& request) override {
    return SendGet(*clients_.at(endpoint), endpoint, request);
  }

 private:
  std::map<std::string, std::shared_ptr<HttpClient>> clients_;
};

}  // namespace

// Sends the requests via the fixed seed peer endpoints.
class ProxyWithEndpointsImpl {
 public:
  ProxyWithEndpointsImpl(std::vector<std::string> endpoints,
                         const ProxyWithEndpointsOptions& options)
      : endpoints_(std::move(endpoints)),
        getter_(std::make_shared<EndpointsSender>(endpoints_),
                RetryPolicy(options.max_retries, options.backoff),
                options.read_timeout) {}

  absl::StatusOr<GetResponse> Get(const GetRequest& request) const {
    if (auto status = Validate(request); !status.ok()) {
      return status;
    }

    return getter_.Get(request, endpoints_);
  }

  absl::StatusOr<GetResponse> GetInto(const GetRequest& request,
                                      std::ostream& output) const {
    if (auto status = Validate(request); !status.ok()) {
      return status;
    }

    return getter_.GetInto(request, endpoints_, output);
  }

 private:
  const std::vector<std::string> endpoints_;
  const ProxyGetter getter_;
};

}  // namespace internal

absl::StatusOr<std::unique_ptr<ProxyWithEndpoints>> ProxyWithEndpoints::Create(
    std::vector<std::string> endpoints,
    const ProxyWithEndpointsOptions& options) {
  if (endpoints.empty()) {
    return absl::InvalidArgumentError("endpoints must not be empty");
  }

  if (options.read_timeout.count() <= 0) {
    return absl::InvalidArgumentError("read timeout must be greater than 0");
  }

  if (options.max_retries > 3) {
    return absl::InvalidArgumentError("max retries must be between 0 and 3");
  }

  // A request is never sent directly, so every endpoint must be a proxy.
  for (const auto& endpoint : endpoints) {
    const auto url = internal::Url::Parse(endpoint);
    if (!url.ok() || !url->IsHttp()) {
      return absl::InvalidArgumentError(absl::StrCat(
          "endpoint \"", endpoint, "\" must be http or https with a host"));
    }
  }

  return absl::WrapUnique(
      new ProxyWithEndpoints(std::make_unique<internal::ProxyWithEndpointsImpl>(
          std::move(endpoints), options)));
}

ProxyWithEndpoints::ProxyWithEndpoints(
    std::unique_ptr<internal::ProxyWithEndpointsImpl> impl)
    : impl_(std::move(impl)) {}

ProxyWithEndpoints::~ProxyWithEndpoints() = default;

absl::StatusOr<GetResponse> ProxyWithEndpoints::Get(const GetRequest& request) {
  return impl_->Get(request);
}

absl::StatusOr<GetResponse> ProxyWithEndpoints::GetInto(
    const GetRequest& request, std::ostream& output) {
  return impl_->GetInto(request, output);
}

}  // namespace dragonfly::client_request
