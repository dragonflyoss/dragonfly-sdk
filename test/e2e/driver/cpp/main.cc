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

// Drives the C++ client-request SDK for the e2e tests. It is the C++
// counterpart of the Go and the Rust drivers and speaks the same command line
// and JSON output, so the e2e suite runs the same specs against every SDK.
//
// Usage:
//
//   driver lookup-endpoints --scheduler <endpoint> <url>
//   driver get --scheduler <endpoint> [--endpoint <endpoint>]...
//       [--header <key: value>]... --output <path> <url>
//   driver preheat --scheduler <endpoint> [--scope <scope>] <url>
//   driver preheat-image --scheduler <endpoint> [--scope <scope>]
//       [--platform <platform>] <image>
//   driver stat-image --scheduler <endpoint> [--platform <platform>] <image>
//   driver delete --scheduler <endpoint> [--scope <scope>] <url>
//   driver delete-image --scheduler <endpoint> [--platform <platform>] <image>

#include <CLI/CLI.hpp>
#include <cstdlib>
#include <fstream>
#include <functional>
#include <iostream>
#include <map>
#include <nlohmann/json.hpp>
#include <optional>
#include <string>
#include <vector>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/ascii.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_split.h"
#include "absl/strings/string_view.h"
#include "dragonfly/client_request/proxy.h"
#include "dragonfly/client_request/proxy_with_endpoints.h"

namespace {

using dragonfly::client_request::DeleteImageRequest;
using dragonfly::client_request::DeleteRequest;
using dragonfly::client_request::GetRequest;
using dragonfly::client_request::GetResponse;
using dragonfly::client_request::PreheatImageRequest;
using dragonfly::client_request::PreheatRequest;
using dragonfly::client_request::Proxy;
using dragonfly::client_request::ProxyWithEndpoints;
using dragonfly::client_request::Scope;
using dragonfly::client_request::StatImageRequest;

/// The command line of the driver. Every command drives an operation of the
/// SDK with its flags and prints the result as JSON.
class Driver {
 public:
  /// Creates a Driver with the commands of the Go and the Rust drivers.
  Driver();

  /// Parses the command line and runs its command, returning the exit code.
  int Run(int argc, char* argv[]);

 private:
  /// A command, run once its flags are parsed.
  using Command = absl::Status (Driver::*)() const;

  /// Adds a command that takes the scheduler endpoint and a url or an image.
  CLI::App* AddCommand(const std::string& name, const std::string& description,
                       const std::string& argument, Command command);

  /// Adds the scope flag to the command, the same values as the Go driver.
  void AddScope(CLI::App* command);

  /// Adds the platform flag to the command.
  void AddPlatform(CLI::App* command);

  /// Looks up the proxy endpoints of the seed peers serving the url and
  /// prints them.
  absl::Status LookupEndpoints() const;

  /// Downloads the url into the output file, via the seed peers the scheduler
  /// picks or via the given endpoints, and prints the response status and
  /// headers.
  absl::Status Get() const;

  /// Preheats the url to the seed peers.
  absl::Status Preheat() const;

  /// Preheats the image to the seed peers, all its platforms unless the
  /// platform is given.
  absl::Status PreheatImage() const;

  /// Queries the layers of the image cached by the seed peers, all its
  /// platforms unless the platform is given, and prints them.
  absl::Status StatImage() const;

  /// Deletes the preheated url from the seed peers.
  absl::Status Delete() const;

  /// Deletes the preheated image from all seed peers, all its platforms
  /// unless the platform is given, and prints the tasks every seed peer
  /// deleted and retained.
  absl::Status DeleteImage() const;

  /// The parser of the command line.
  CLI::App app_{"Drives the C++ client-request SDK for the e2e tests."};

  /// The scheduler endpoint.
  std::string scheduler_endpoint_;

  /// The seed peer endpoints to send the request to.
  std::vector<std::string> endpoints_;

  /// The request headers, each as "Key: Value".
  std::vector<std::string> headers_;

  /// The output file path.
  std::string output_;

  /// The seed peers to address.
  Scope scope_ = Scope::kDefault;

  /// The platform of the image as "os/arch", all the platforms when unset.
  std::optional<std::string> platform_;

  /// The url or the image of the command.
  std::string argument_;

  /// The result of the command.
  absl::Status status_;
};

Driver::Driver() {
  app_.require_subcommand(1);

  AddCommand("lookup-endpoints",
             "Looks up the proxy endpoints of the seed peers serving the url",
             "url", &Driver::LookupEndpoints);

  auto* get = AddCommand("get", "Downloads the url into the output file", "url",
                         &Driver::Get);
  get->add_option("--endpoint", endpoints_,
                  "Seed peer endpoint to send the request to, repeatable")
      ->allow_extra_args(false);
  get->add_option("--header", headers_,
                  "Request header 'Key: Value', repeatable")
      ->allow_extra_args(false);
  get->add_option("--output", output_, "Output file path")->required();

  AddScope(AddCommand("preheat", "Preheats the url to the seed peers", "url",
                      &Driver::Preheat));

  auto* preheat_image =
      AddCommand("preheat-image", "Preheats the image to the seed peers",
                 "image", &Driver::PreheatImage);
  AddScope(preheat_image);
  AddPlatform(preheat_image);

  AddPlatform(AddCommand(
      "stat-image", "Queries the layers of the image cached by the seed peers",
      "image", &Driver::StatImage));

  AddScope(AddCommand("delete", "Deletes the preheated url from the seed peers",
                      "url", &Driver::Delete));

  AddPlatform(AddCommand("delete-image",
                         "Deletes the preheated image from all seed peers",
                         "image", &Driver::DeleteImage));
}

int Driver::Run(int argc, char* argv[]) {
  CLI11_PARSE(app_, argc, argv);
  if (!status_.ok()) {
    std::cerr << status_ << "\n";
    return EXIT_FAILURE;
  }

  return EXIT_SUCCESS;
}

CLI::App* Driver::AddCommand(const std::string& name,
                             const std::string& description,
                             const std::string& argument, Command command) {
  auto* subcommand = app_.add_subcommand(name, description);
  subcommand->add_option("--scheduler", scheduler_endpoint_,
                         "Scheduler endpoint");
  subcommand->add_option(argument, argument_)->required();
  subcommand->callback(
      [this, command] { status_ = std::invoke(command, this); });
  return subcommand;
}

void Driver::AddScope(CLI::App* command) {
  command->add_option("--scope", scope_, "Seed peers to address")
      ->transform(CLI::CheckedTransformer(std::map<std::string, Scope>{
          {"default", Scope::kDefault},
          {"all_seed_peers", Scope::kAllSeedPeers}}));
}

void Driver::AddPlatform(CLI::App* command) {
  command->add_option("--platform", platform_,
                      "Platform of the image in the format os/arch, all the "
                      "platforms by default");
}

absl::Status Driver::LookupEndpoints() const {
  auto proxy = Proxy::Create(scheduler_endpoint_);
  if (!proxy.ok()) {
    return proxy.status();
  }

  GetRequest request;
  request.url = argument_;
  auto endpoints = (*proxy)->LookupEndpoints(request);
  if (!endpoints.ok()) {
    return endpoints.status();
  }

  std::cout << nlohmann::json{{"endpoints", *endpoints}} << "\n";
  return absl::OkStatus();
}

absl::Status Driver::Get() const {
  GetRequest request;
  request.url = argument_;
  for (const auto& header : headers_) {
    const std::vector<absl::string_view> key_value =
        absl::StrSplit(header, absl::MaxSplits(':', 1));
    if (key_value.size() != 2) {
      return absl::InvalidArgumentError(
          absl::StrCat("invalid header '", header, "', expected 'Key: Value'"));
    }

    request.header.emplace(absl::StripAsciiWhitespace(key_value[0]),
                           absl::StripAsciiWhitespace(key_value[1]));
  }

  std::ofstream output(output_, std::ios::binary);
  if (!output) {
    return absl::InternalError(absl::StrCat("failed to create ", output_));
  }

  absl::StatusOr<GetResponse> response;
  if (endpoints_.empty()) {
    auto proxy = Proxy::Create(scheduler_endpoint_);
    if (!proxy.ok()) {
      return proxy.status();
    }

    response = (*proxy)->GetInto(request, output);
  } else {
    auto proxy_with_endpoints = ProxyWithEndpoints::Create(endpoints_);
    if (!proxy_with_endpoints.ok()) {
      return proxy_with_endpoints.status();
    }

    response = (*proxy_with_endpoints)->GetInto(request, output);
  }

  if (!response.ok()) {
    return response.status();
  }

  output.close();
  if (!output) {
    return absl::InternalError(absl::StrCat("failed to write ", output_));
  }

  std::map<std::string, std::string> header;
  for (const auto& [key, value] : response->header) {
    header.insert_or_assign(key, value);
  }

  std::cout << nlohmann::json{{"status_code", response->status_code},
                              {"header", header}}
            << "\n";
  return absl::OkStatus();
}

absl::Status Driver::Preheat() const {
  auto proxy = Proxy::Create(scheduler_endpoint_);
  if (!proxy.ok()) {
    return proxy.status();
  }

  PreheatRequest request;
  request.url = argument_;
  request.scope = scope_;
  return (*proxy)->Preheat(request);
}

absl::Status Driver::PreheatImage() const {
  auto proxy = Proxy::Create(scheduler_endpoint_);
  if (!proxy.ok()) {
    return proxy.status();
  }

  PreheatImageRequest request;
  request.image = argument_;
  request.platform = platform_;
  request.scope = scope_;
  return (*proxy)->PreheatImage(request);
}

absl::Status Driver::StatImage() const {
  auto proxy = Proxy::Create(scheduler_endpoint_);
  if (!proxy.ok()) {
    return proxy.status();
  }

  StatImageRequest request;
  request.image = argument_;
  request.platform = platform_;
  auto response = (*proxy)->StatImage(request);
  if (!response.ok()) {
    return response.status();
  }

  auto peers = nlohmann::json::array();
  for (const auto& peer : response->peers) {
    auto cached_layers = nlohmann::json::array();
    for (const auto& layer : peer.cached_layers) {
      cached_layers.push_back(
          {{"url", layer.url}, {"is_finished", layer.is_finished}});
    }

    peers.push_back({{"ip", peer.ip},
                     {"hostname", peer.hostname},
                     {"cached_layers", cached_layers}});
  }

  std::cout << nlohmann::json{{"layers", response->layers}, {"peers", peers}}
            << "\n";
  return absl::OkStatus();
}

absl::Status Driver::Delete() const {
  auto proxy = Proxy::Create(scheduler_endpoint_);
  if (!proxy.ok()) {
    return proxy.status();
  }

  DeleteRequest request;
  request.url = argument_;
  request.scope = scope_;
  return (*proxy)->Delete(request);
}

absl::Status Driver::DeleteImage() const {
  auto proxy = Proxy::Create(scheduler_endpoint_);
  if (!proxy.ok()) {
    return proxy.status();
  }

  DeleteImageRequest request;
  request.image = argument_;
  request.platform = platform_;
  auto response = (*proxy)->DeleteImage(request);
  if (!response.ok()) {
    return response.status();
  }

  auto peers = nlohmann::json::array();
  for (const auto& peer : response->peers) {
    peers.push_back({{"ip", peer.ip},
                     {"hostname", peer.hostname},
                     {"deleted_task_ids", peer.deleted_task_ids},
                     {"retained_task_ids", peer.retained_task_ids}});
  }

  std::cout << nlohmann::json{{"peers", peers}} << "\n";
  return absl::OkStatus();
}

}  // namespace

int main(int argc, char* argv[]) { return Driver().Run(argc, argv); }
