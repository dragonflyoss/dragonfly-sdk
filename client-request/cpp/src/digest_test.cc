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

#include "src/digest.h"

#include <gtest/gtest.h>

#include <utility>

namespace dragonfly::client_request::internal {
namespace {

constexpr char kSha256[] =
    "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e";
constexpr char kSha512[] =
    "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32"
    "bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8";

struct TestCase {
  std::string url;
  std::string algorithm;
  std::string encoded;
};

void ExpectDigest(const std::optional<Digest>& digest,
                  const TestCase& test_case) {
  ASSERT_EQ(digest.has_value(), !test_case.algorithm.empty()) << test_case.url;
  if (digest) {
    EXPECT_EQ(digest->algorithm, test_case.algorithm) << test_case.url;
    EXPECT_EQ(digest->encoded, test_case.encoded) << test_case.url;
  }
}

TEST(DigestTest, ExtractDigestFromBlobUrlParsesOciBlobDigests) {
  const TestCase test_cases[] = {
      {"http://registry.example.com/v2/library/ubuntu/blobs/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       "sha256", kSha256},
      {"https://registry.example.com/v2/myorg/myrepo/blobs/sha512:"
       "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9a"
       "e32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8",
       "sha512", kSha512},
      {"https://registry.io/v2/org/team/project/blobs/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       "sha256", kSha256},
      {"http://localhost:5000/v2/myrepo/blobs/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
       "?ns=docker.io",
       "sha256", kSha256},
      {"http://registry.example.com/blobs/sha256:abc", "", ""},
      {"http://registry.example.com/v2/repo/manifests/sha256:abc", "", ""},
      {"registry.example.com/v2/repo/blobs/sha256:abc", "", ""},
      {"http://registry.example.com/v2/blobs/sha256:abc", "", ""},
      {"", "", ""},
      {"not-a-url", "", ""},
      {"http://registry.example.com/v2/repo/blobs/invalid-digest", "", ""},
      {"http://registry.example.com/v2/repo/blobs/"
       "md5:8a04994a666b4e4b20a2fd9e5a44f44c",
       "", ""},
  };

  for (const auto& test_case : test_cases) {
    ExpectDigest(ExtractDigestFromBlobUrl(test_case.url), test_case);
  }
}

TEST(DigestTest, IsBlobUrlMatchesOciBlobUrls) {
  const std::pair<const char*, bool> test_cases[] = {
      {"http://registry.example.com/v2/library/ubuntu/blobs/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       true},
      {"http://registry.example.com/v2/library/ubuntu/blobs/sha256:abc", true},
      {"http://registry.example.com/v2/library/ubuntu/blobs/"
       "md5:8a04994a666b4e4b20a2fd9e5a44f44c",
       true},
      {"http://registry.example.com/v2/library/ubuntu/manifests/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       false},
      {"http://registry.example.com/v2/blobs/sha256:abc", false},
      {"https://example.com/file.txt", false},
  };

  for (const auto& [url, expected] : test_cases) {
    EXPECT_EQ(IsBlobUrl(url), expected) << url;
  }
}

TEST(DigestTest, IsManifestDigestUrlMatchesOciManifestDigestUrls) {
  const std::pair<const char*, bool> test_cases[] = {
      {"http://registry.example.com/v2/library/ubuntu/manifests/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       true},
      {"http://localhost:5000/v2/myrepo/manifests/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
       "?ns=docker.io",
       true},
      {"http://registry.example.com/v2/library/ubuntu/manifests/latest", false},
      {"http://registry.example.com/v2/library/ubuntu/manifests/"
       "md5:8a04994a666b4e4b20a2fd9e5a44f44c",
       false},
      {"http://registry.example.com/v2/library/ubuntu/manifests/sha256:abc",
       false},
      {"http://registry.example.com/v2/library/ubuntu/blobs/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       false},
      {"https://example.com/file.txt", false},
  };

  for (const auto& [url, expected] : test_cases) {
    EXPECT_EQ(IsManifestDigestUrl(url), expected) << url;
  }
}

TEST(DigestTest, ExtractDigestFromManifestUrlParsesOciManifestDigests) {
  const TestCase test_cases[] = {
      {"http://registry.example.com/v2/library/ubuntu/manifests/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       "sha256", kSha256},
      {"http://localhost:5000/v2/myrepo/manifests/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
       "?ns=docker.io",
       "sha256", kSha256},
      {"http://registry.example.com/v2/library/ubuntu/manifests/latest", "",
       ""},
      {"http://registry.example.com/v2/library/ubuntu/manifests/sha256:abc", "",
       ""},
      {"http://registry.example.com/v2/library/ubuntu/manifests/"
       "md5:8a04994a666b4e4b20a2fd9e5a44f44c",
       "", ""},
      {"http://registry.example.com/v2/library/ubuntu/blobs/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       "", ""},
      {"https://example.com/file.txt", "", ""},
  };

  for (const auto& test_case : test_cases) {
    ExpectDigest(ExtractDigestFromManifestUrl(test_case.url), test_case);
  }
}

}  // namespace
}  // namespace dragonfly::client_request::internal
