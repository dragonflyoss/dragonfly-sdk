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

#include "src/id_generator.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <algorithm>

#include "dragonfly/client_request/request.h"
#include "src/hashring.h"

namespace dragonfly::client_request::internal {
namespace {

using ::testing::ElementsAreArray;

constexpr char kBlobUrl[] =
    "http://registry.example.com/v2/library/ubuntu/blobs/sha256:"
    "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e";
constexpr char kSha256[] =
    "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e";
constexpr char kSha512[] =
    "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32"
    "bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8";

TEST(IdGeneratorTest, TaskIdMatchesTheGoAndRustClients) {
  struct TestCase {
    std::string url;
    std::optional<std::uint64_t> piece_length;
    std::string tag;
    std::string application;
    std::vector<std::string> filtered_query_params;
    std::string content;
    std::string expected;
  };
  const TestCase test_cases[] = {
      {"https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar",
       4194304,
       "tag1",
       "app1",
       {"Expires", "Signature"},
       "",
       "2a0c4c713d7f2f65f36b78b79c4b78a6bf5d5f67b76730ed13485d3271482f1c"},
      {"https://example.com/file.txt",
       std::nullopt,
       "",
       "",
       {},
       "",
       "7fcf06e5f0b1e443065c1a563eed788eb2e168a05c6ad9c4b319f7a976322be0"},
      {"https://example.com",
       std::nullopt,
       "foo",
       "bar",
       {},
       "",
       "06408fbf247ddaca478f8cb9565fe5591c28efd0994b8fea80a6a87d3203c5ca"},
      {"https://example.com",
       std::nullopt,
       "foo",
       "",
       {},
       "",
       "3c3f230ef9f191dd2821510346a7bc138e4894bee9aee184ba250a3040701d2a"},
      {"https://example.com",
       std::nullopt,
       "",
       "bar",
       {},
       "",
       "c9f9261b7305c24371244f9f149f5d4589ed601348fdf22d7f6f4b10658fdba2"},
      {"https://example.com",
       1024,
       "",
       "",
       {},
       "",
       "9f7c9aafbc6f30f8f41a96ca77eeae80c5b60964b3034b0ee43ccf7b2f9e52b8"},
      {"https://example.com?foo=foo&bar=bar",
       std::nullopt,
       "",
       "",
       {"foo", "bar"},
       "",
       "457b4328cde278e422c9e243f7bfd1e97f511fec43a80f535cf6b0ef6b086776"},
      {"https://example.com/file.txt?z=9&b=2&a=1",
       std::nullopt,
       "foo",
       "bar",
       {"z"},
       "",
       "8b3f6e9b9b8fe20903bced565cfd1d0aaef354a4c17573f0c2c1979210443f9d"},
      {"https://example.com/file.txt?b=2&a=1&b=1",
       std::nullopt,
       "",
       "",
       {"c"},
       "",
       "7c8801d0596be5e8f9449d5c4af23866c72fe5205119c0e5912981f3b16a37aa"},
      {"https://example.com/file.txt?k=a b&m=x*y&n=c~d",
       1024,
       "",
       "",
       {"none"},
       "",
       "6196a6846023f6d3c1e4d30f6c86f3d4186e4c664a33e5692b0e04e49b26a9af"},
      {"https://example.com/file.txt?a=1&b=2",
       std::nullopt,
       "foo",
       "",
       {"a", "b"},
       "",
       "c8f4b41117329d54af920010394f6f607bac707e933ab2f18d372e3dd4c7fcb3"},
      {"https://example.com/file.txt?b=2&a=1",
       std::nullopt,
       "",
       "",
       {},
       "",
       "980ee327518ccc5a7c30703e1a2232e8ba9047b39431f940636c85b6146f8b9a"},
      {"https://example.com/file.txt?a=1;x&b=2",
       std::nullopt,
       "",
       "",
       {"none"},
       "",
       "370933e9d7ab42b3ed90287213cd5f8def6091c8606da5b2dc79436061b06c34"},
      {"http://www.xx.yy/path?u=f&x=y&m=z&x=s#size",
       std::nullopt,
       "",
       "",
       {"x", "m"},
       "",
       "3570c3c808f06fd250a7a60634b9275d72c56edc248576508bb06264c2c65825"},
      {"",
       std::nullopt,
       "",
       "",
       {},
       "This is a test file",
       "e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249"},
      {kBlobUrl, std::nullopt, "", "", {}, "", kSha256},
      {"https://registry.example.com/v2/myorg/myrepo/blobs/sha512:"
       "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9a"
       "e32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8",
       std::nullopt,
       "",
       "",
       {},
       "",
       kSha512},
      {"http://localhost:5000/v2/myrepo/blobs/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
       "?ns=docker.io",
       std::nullopt,
       "",
       "",
       {},
       "",
       kSha256},
      {"http://registry.example.com/v2/library/ubuntu/manifests/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
       std::nullopt,
       "",
       "",
       {},
       "",
       kSha256},
      {"https://registry.example.com/v2/myorg/myrepo/manifests/sha512:"
       "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9a"
       "e32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8",
       std::nullopt,
       "",
       "",
       {},
       "",
       kSha512},
      {"http://localhost:5000/v2/myrepo/manifests/sha256:"
       "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
       "?ns=docker.io",
       std::nullopt,
       "",
       "",
       {},
       "",
       kSha256},
      {"http://registry.example.com/v2/library/ubuntu/manifests/latest",
       std::nullopt,
       "",
       "",
       {},
       "",
       "0b6500b31c7eea2929393e154299bc81ebbb613c24fa7f5f33e893d585f4d629"},
  };

  for (const auto& test_case : test_cases) {
    auto id = TaskId(test_case.url, test_case.piece_length, test_case.tag,
                     test_case.application, test_case.filtered_query_params,
                     test_case.content, true);
    ASSERT_TRUE(id.ok()) << test_case.url << ": " << id.status();
    EXPECT_EQ(*id, test_case.expected) << test_case.url;
  }
}

TEST(IdGeneratorTest, TaskIdFailsOnAnInvalidBlobDigestOrUrl) {
  for (const char* url :
       {"http://registry.example.com/v2/library/ubuntu/blobs/sha256:abc",
        "http://registry.example.com/v2/library/ubuntu/blobs/"
        "md5:8a04994a666b4e4b20a2fd9e5a44f44c",
        "example.com/file.txt"}) {
    EXPECT_EQ(
        TaskId(url, std::nullopt, "", "", {"ns"}, "", true).status().code(),
        absl::StatusCode::kInternal)
        << url;
  }
}

TEST(IdGeneratorTest, TaskIdSelectsTheSeedPeersOfTheRustClient) {
  struct TestCase {
    std::string url;
    std::optional<std::uint64_t> piece_length;
    std::string tag;
    std::string application;
    std::vector<std::string> filtered_query_params;
    std::string content;
    bool enable_task_id_based_blob_digest;
    std::size_t replicas;
    std::vector<std::string> expected;
  };
  const TestCase test_cases[] = {
      {"https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar",
       4194304,
       "tag-a",
       "app-a",
       {"Expires", "Signature"},
       "",
       true,
       3,
       {"seed-peer-3", "seed-peer-2", "seed-peer-1"}},
      {"https://example.com/file.txt?Expires=e2&Signature=s2&foo=bar",
       4194304,
       "tag-a",
       "app-a",
       {"Expires", "Signature"},
       "",
       true,
       3,
       {"seed-peer-3", "seed-peer-2", "seed-peer-1"}},
      {"https://example.com/file.txt",
       std::nullopt,
       "",
       "",
       DefaultFilteredQueryParams(),
       "",
       true,
       2,
       {"seed-peer-1", "seed-peer-2"}},
      {"https://example.com/file.txt",
       std::nullopt,
       "",
       "",
       DefaultFilteredQueryParams(),
       "",
       true,
       1,
       {"seed-peer-1"}},
      {"https://example.com/file.txt",
       std::nullopt,
       "tag-a",
       "",
       DefaultFilteredQueryParams(),
       "",
       true,
       2,
       {"seed-peer-3", "seed-peer-2"}},
      {"https://example.com/file.txt",
       std::nullopt,
       "tag-b",
       "",
       DefaultFilteredQueryParams(),
       "",
       true,
       2,
       {"seed-peer-2", "seed-peer-1"}},
      {"https://example.com/file.txt",
       std::nullopt,
       "",
       "app-a",
       DefaultFilteredQueryParams(),
       "",
       true,
       2,
       {"seed-peer-1", "seed-peer-3"}},
      {"https://example.com/file.txt",
       std::nullopt,
       "",
       "",
       DefaultFilteredQueryParams(),
       "This is a test file",
       true,
       3,
       {"seed-peer-2", "seed-peer-3", "seed-peer-1"}},
      {kBlobUrl,
       std::nullopt,
       "",
       "",
       DefaultFilteredQueryParams(),
       "",
       true,
       3,
       {"seed-peer-3", "seed-peer-2", "seed-peer-1"}},
      {kBlobUrl,
       std::nullopt,
       "",
       "",
       DefaultFilteredQueryParams(),
       "",
       false,
       2,
       {"seed-peer-3", "seed-peer-1"}},
  };

  VNodeHashRing ring;
  for (const char* name : {"seed-peer-1", "seed-peer-2", "seed-peer-3"}) {
    ring.Add(name);
  }

  for (const auto& test_case : test_cases) {
    auto id =
        TaskId(test_case.url, test_case.piece_length, test_case.tag,
               test_case.application, test_case.filtered_query_params,
               test_case.content, test_case.enable_task_id_based_blob_digest);
    ASSERT_TRUE(id.ok()) << test_case.url << ": " << id.status();

    std::vector<std::string> selected;
    for (const auto& name : ring.GetWithReplicas(*id, ring.size())) {
      if (selected.size() < test_case.replicas &&
          std::find(selected.begin(), selected.end(), name) == selected.end()) {
        selected.push_back(name);
      }
    }
    EXPECT_THAT(selected, ElementsAreArray(test_case.expected))
        << test_case.url;
  }
}

}  // namespace
}  // namespace dragonfly::client_request::internal
