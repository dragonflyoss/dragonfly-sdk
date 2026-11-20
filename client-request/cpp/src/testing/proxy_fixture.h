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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_PROXY_FIXTURE_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_PROXY_FIXTURE_H_

#include <gtest/gtest.h>

#include <chrono>
#include <memory>
#include <set>
#include <string>
#include <utility>
#include <vector>

#include "absl/strings/str_cat.h"
#include "src/proxy_impl.h"
#include "src/testing/fake_registry.h"
#include "src/testing/fake_servers.h"

namespace dragonfly::client_request::internal::testing {
inline constexpr char kImage[] = "registry.example.com/library/nginx:latest";
inline constexpr char kOciManifest[] =
    "application/vnd.oci.image.manifest.v1+json";
inline constexpr char kOciIndex[] = "application/vnd.oci.image.index.v1+json";
inline constexpr char kAmd64Digest[] =
    "sha256:1111111111111111111111111111111111111111111111111111111111111111";
inline constexpr char kArm64Digest[] =
    "sha256:2222222222222222222222222222222222222222222222222222222222222222";
inline constexpr char kConfigAmd64[] =
    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa";
inline constexpr char kConfigArm64[] =
    "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb";
inline constexpr char kShared[] =
    "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc";
inline constexpr char kAmd64[] =
    "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd";
inline constexpr char kArm64[] =
    "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee";

inline std::string ManifestUrl(const std::string& reference) {
  return absl::StrCat(
      "https://registry.example.com/v2/library/nginx/manifests/", reference);
}

inline std::string BlobUrl(const std::string& digest) {
  return absl::StrCat("https://registry.example.com/v2/library/nginx/blobs/",
                      digest);
}

class ProxyFixture : public ::testing::Test {
 protected:
  ProxyFixture()
      : proxy_(RetryPolicy(), std::chrono::seconds(5), registry_.getter()) {
    registry_.Answer("https://registry.example.com/v2/", 401, "",
                     {{"www-authenticate",
                       R"(Bearer realm="https://auth.example.com/token")"}});
    registry_.Answer(
        "https://auth.example.com/token?scope=repository%3Alibrary%2Fnginx"
        "%3Apull",
        200, R"({"token":"abc","expires_in":300})");
    registry_.AnswerManifest(
        ManifestUrl("latest"), kOciIndex,
        absl::StrCat(
            R"({"schemaVersion":2,"manifests":[)", R"({"digest":")",
            kAmd64Digest,
            R"(","platform":{"os":"linux","architecture":"amd64"}},)",
            R"({"digest":")", kArm64Digest,
            R"(","platform":{"os":"linux","architecture":"arm64"}}]})"));
    registry_.AnswerManifest(ManifestUrl(kAmd64Digest), kOciManifest,
                             ImageManifest(kConfigAmd64, {kShared, kAmd64}));
    registry_.AnswerManifest(ManifestUrl(kArm64Digest), kOciManifest,
                             ImageManifest(kConfigArm64, {kShared, kArm64}));
  }

  void AddSeedPeers(int count) {
    std::vector<SeedPeer> seed_peers;
    for (int i = 1; i <= count; ++i) {
      fakes_.push_back(
          std::make_unique<FakeSeedPeer>("seed-peer-" + std::to_string(i)));
      seed_peers.push_back(fakes_.back()->seed_peer());
    }

    proxy_.selector().Update(seed_peers);
  }

  int DownloadTaskCalls() const {
    int calls = 0;
    for (const auto& fake : fakes_) {
      calls += fake->download_task_calls;
    }

    return calls;
  }

  std::vector<dfdaemon::v2::DownloadTaskRequest> DownloadTaskRequests() const {
    std::vector<dfdaemon::v2::DownloadTaskRequest> requests;
    for (const auto& fake : fakes_) {
      for (auto& request : fake->download_task_requests()) {
        requests.push_back(std::move(request));
      }
    }

    return requests;
  }

  FakeRegistry registry_;
  ProxyImpl proxy_;
  std::vector<std::unique_ptr<FakeSeedPeer>> fakes_;
};

}  // namespace dragonfly::client_request::internal::testing

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_PROXY_FIXTURE_H_
