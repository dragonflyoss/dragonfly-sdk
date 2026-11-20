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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_FAKE_REGISTRY_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_FAKE_REGISTRY_H_

#include <map>
#include <mutex>
#include <string>
#include <utility>
#include <vector>

#include "absl/strings/str_cat.h"
#include "src/oci.h"

namespace dragonfly::client_request::internal::testing {
class FakeRegistry {
 public:
  void Answer(const std::string& url, int status_code, std::string body,
              Header header = {}) {
    answers_[url] = HttpResult{status_code, std::move(header), std::move(body)};
  }

  void AnswerManifest(const std::string& url, const std::string& media_type,
                      std::string body) {
    Answer(url, 200, std::move(body), {{"content-type", media_type}});
  }

  HttpGetter getter() {
    return [this](const std::string& url,
                  const Header& header) -> absl::StatusOr<HttpResult> {
      std::lock_guard<std::mutex> lock(mu_);
      requests.emplace_back(url, header);
      const auto it = answers_.find(url);
      if (it == answers_.end()) {
        return HttpResult{404, {}, "not found"};
      }

      return it->second;
    };
  }

  int Count(const std::string& url) const {
    int count = 0;
    for (const auto& request : requests) {
      count += request.first == url ? 1 : 0;
    }

    return count;
  }

  std::string AuthorizationOf(const std::string& url) const {
    for (const auto& request : requests) {
      const auto it = request.second.find("Authorization");
      if (request.first == url && it != request.second.end()) {
        return it->second;
      }
    }

    return "";
  }

  std::vector<std::pair<std::string, Header>> requests;

 private:
  std::mutex mu_;
  std::map<std::string, HttpResult> answers_;
};

inline std::string ImageManifest(const std::string& config,
                                 const std::vector<std::string>& layers) {
  std::string manifest = absl::StrCat(
      R"({"schemaVersion":2,"config":{"digest":")", config, R"("},"layers":[)");
  for (std::size_t i = 0; i < layers.size(); ++i) {
    absl::StrAppend(&manifest, i == 0 ? "" : ",", R"({"digest":")", layers[i],
                    R"("})");
  }

  return absl::StrCat(manifest, "]}");
}

}  // namespace dragonfly::client_request::internal::testing

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_FAKE_REGISTRY_H_
