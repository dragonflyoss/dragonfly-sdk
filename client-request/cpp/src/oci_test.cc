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

#include "src/oci.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include <map>
#include <string>
#include <utility>
#include <vector>

#include "absl/strings/escaping.h"
#include "absl/strings/str_cat.h"
#include "src/testing/fake_registry.h"

namespace dragonfly::client_request::internal {
namespace {

using ::testing::ElementsAre;
using testing::FakeRegistry;
using ::testing::HasSubstr;
using testing::ImageManifest;
using ::testing::SizeIs;

constexpr char kOciManifest[] = "application/vnd.oci.image.manifest.v1+json";
constexpr char kOciIndex[] = "application/vnd.oci.image.index.v1+json";
constexpr char kAmd64Digest[] =
    "sha256:1111111111111111111111111111111111111111111111111111111111111111";
constexpr char kArm64Digest[] =
    "sha256:2222222222222222222222222222222222222222222222222222222222222222";
constexpr char kAttestationDigest[] =
    "sha256:3333333333333333333333333333333333333333333333333333333333333333";

Reference TestReference() {
  return *ParseImage("registry.example.com/library/nginx:latest");
}

TEST(OciTest, ParseImageNormalizesTheReference) {
  struct TestCase {
    std::string image;
    std::string registry;
    std::string repository;
    std::string reference;
  };

  const std::string digest =
      "sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e";
  const TestCase test_cases[] = {
      {"nginx", "registry-1.docker.io", "library/nginx", "latest"},
      {"nginx:latest", "registry-1.docker.io", "library/nginx", "latest"},
      {"dragonflyoss/client:v1.0.0", "registry-1.docker.io",
       "dragonflyoss/client", "v1.0.0"},
      {"docker.io/library/nginx", "registry-1.docker.io", "library/nginx",
       "latest"},
      {"index.docker.io/library/nginx:1.27", "registry-1.docker.io",
       "library/nginx", "1.27"},
      {"ghcr.io/dragonflyoss/client:v1.5.12", "ghcr.io", "dragonflyoss/client",
       "v1.5.12"},
      {"localhost:5000/myrepo:v1.0.0", "localhost:5000", "myrepo", "v1.0.0"},
      {"localhost/myrepo", "localhost", "myrepo", "latest"},
      {"registry.example.com/library/nginx@" + digest, "registry.example.com",
       "library/nginx", digest},
      {"registry.example.com/library/nginx:1.27@" + digest,
       "registry.example.com", "library/nginx", digest},
  };

  for (const auto& test_case : test_cases) {
    auto reference = ParseImage(test_case.image);
    ASSERT_TRUE(reference.ok())
        << test_case.image << ": " << reference.status();
    EXPECT_EQ(reference->scheme, "https");
    EXPECT_EQ(reference->registry, test_case.registry) << test_case.image;
    EXPECT_EQ(reference->repository, test_case.repository) << test_case.image;
    EXPECT_EQ(reference->reference, test_case.reference) << test_case.image;
  }
}

TEST(OciTest, ParseImageRejectsAnInvalidReference) {
  const std::string test_cases[] = {
      "invalid image reference!!",
      "INVALID IMAGE",
      "",
      "registry.example.com/Library/nginx",
      "nginx:",
      "nginx@sha256:abc",
      "registry.example.com/library//nginx",
  };

  for (const auto& image : test_cases) {
    auto reference = ParseImage(image);
    EXPECT_EQ(reference.status().code(), absl::StatusCode::kInvalidArgument)
        << image;
    EXPECT_THAT(std::string(reference.status().message()),
                HasSubstr("invalid image reference"));
  }
}

TEST(OciTest, ReferenceBuildsTheUrls) {
  Reference reference = TestReference();
  EXPECT_EQ(reference.ManifestUrl(),
            "https://registry.example.com/v2/library/nginx/manifests/latest");
  EXPECT_EQ(reference.BlobUrl("sha256:abc"),
            "https://registry.example.com/v2/library/nginx/blobs/sha256:abc");
  EXPECT_EQ(reference.ToString(), "registry.example.com/library/nginx:latest");

  reference.reference = "sha256:abc";
  EXPECT_EQ(reference.ToString(),
            "registry.example.com/library/nginx@sha256:abc");
}

TEST(OciTest, AuthorizationIsEmptyWithoutAChallenge) {
  FakeRegistry registry;
  registry.Answer("https://registry.example.com/v2/", 200, "{}");

  auto client =
      RegistryClient::Create(TestReference(), registry.getter(), "", "");
  ASSERT_TRUE(client.ok()) << client.status();
  auto authorization = (*client)->Authorization();
  ASSERT_TRUE(authorization.ok()) << authorization.status();
  EXPECT_EQ(*authorization, "");
}

TEST(OciTest, AuthorizationFetchesAndKeepsTheBearerToken) {
  const std::string token_url =
      "https://auth.example.com/token?scope=repository%3Alibrary%2Fnginx%3Apull"
      "&service=registry.example.com";
  FakeRegistry registry;
  registry.Answer(
      "https://registry.example.com/v2/", 401, "",
      {{"www-authenticate",
        R"(Bearer realm="https://auth.example.com/token",service="registry.example.com")"}});
  registry.Answer(token_url, 200, R"({"token":"abc","expires_in":300})");

  auto client =
      RegistryClient::Create(TestReference(), registry.getter(), "", "");
  ASSERT_TRUE(client.ok()) << client.status();
  for (int i = 0; i < 3; ++i) {
    auto authorization = (*client)->Authorization();
    ASSERT_TRUE(authorization.ok()) << authorization.status();
    EXPECT_EQ(*authorization, "Bearer abc");
  }

  EXPECT_EQ(registry.Count(token_url), 1);
  EXPECT_EQ(registry.AuthorizationOf(token_url), "");
}

TEST(OciTest, AuthorizationRenewsAnExpiredToken) {
  const std::string token_url =
      "https://auth.example.com/"
      "token?scope=repository%3Alibrary%2Fnginx%3Apull";
  FakeRegistry registry;
  registry.Answer("https://registry.example.com/v2/", 401, "",
                  {{"www-authenticate",
                    R"(Bearer realm="https://auth.example.com/token")"}});
  registry.Answer(
      token_url, 200,
      R"({"access_token":"abc","expires_in":60,"issued_at":"2020-01-01T00:00:00Z"})");

  auto client =
      RegistryClient::Create(TestReference(), registry.getter(), "", "");
  ASSERT_TRUE(client.ok()) << client.status();
  EXPECT_EQ(*(*client)->Authorization(), "Bearer abc");
  EXPECT_EQ(*(*client)->Authorization(), "Bearer abc");
  EXPECT_EQ(registry.Count(token_url), 2);
}

TEST(OciTest, AuthorizationSendsTheCredentialsForTheToken) {
  const std::string token_url =
      "https://auth.example.com/token?account=user&scope=repository%3Alibrary"
      "%2Fnginx%3Apull&service=registry.example.com";
  FakeRegistry registry;
  registry.Answer(
      "https://registry.example.com/v2/", 401, "",
      {{"www-authenticate",
        R"(Bearer realm="https://auth.example.com/token",service="registry.example.com")"}});
  registry.Answer(token_url, 200, R"({"token":"abc"})");

  auto client = RegistryClient::Create(TestReference(), registry.getter(),
                                       "user", "secret");
  ASSERT_TRUE(client.ok()) << client.status();
  EXPECT_EQ(*(*client)->Authorization(), "Bearer abc");
  EXPECT_EQ(registry.AuthorizationOf(token_url),
            "Basic " + absl::Base64Escape("user:secret"));
}

TEST(OciTest, AuthorizationFailsWhenTheTokenIsRefused) {
  FakeRegistry registry;
  registry.Answer("https://registry.example.com/v2/", 401, "",
                  {{"www-authenticate",
                    R"(Bearer realm="https://auth.example.com/token")"}});
  registry.Answer(
      "https://auth.example.com/"
      "token?scope=repository%3Alibrary%2Fnginx%3Apull",
      401, "denied");

  auto client =
      RegistryClient::Create(TestReference(), registry.getter(), "", "");
  ASSERT_TRUE(client.ok()) << client.status();
  auto authorization = (*client)->Authorization();
  EXPECT_EQ(authorization.status().code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(authorization.status().message()), HasSubstr("401"));
}

TEST(OciTest, AuthorizationIsTheBasicCredentialsForABasicChallenge) {
  FakeRegistry registry;
  registry.Answer("https://registry.example.com/v2/", 401, "",
                  {{"www-authenticate", R"(Basic realm="registry")"}});

  auto client = RegistryClient::Create(TestReference(), registry.getter(),
                                       "user", "secret");
  ASSERT_TRUE(client.ok()) << client.status();
  EXPECT_EQ(*(*client)->Authorization(),
            "Basic " + absl::Base64Escape("user:secret"));

  client = RegistryClient::Create(TestReference(), registry.getter(), "", "");
  ASSERT_TRUE(client.ok()) << client.status();
  EXPECT_FALSE((*client)->Authorization().ok());
}

TEST(OciTest, CreateFailsWhenTheRegistryIsUnreachable) {
  HttpGetter getter = [](const std::string&,
                         const Header&) -> absl::StatusOr<HttpResult> {
    return absl::InternalError("connection refused");
  };

  EXPECT_EQ(
      RegistryClient::Create(TestReference(), getter, "", "").status().code(),
      absl::StatusCode::kInternal);
}

class ResolveTest : public ::testing::Test {
 protected:
  ResolveTest() {
    registry_.Answer("https://registry.example.com/v2/", 401, "",
                     {{"www-authenticate",
                       R"(Bearer realm="https://auth.example.com/token")"}});
    registry_.Answer(
        "https://auth.example.com/token?scope=repository%3Alibrary%2Fnginx"
        "%3Apull",
        200, R"({"token":"abc","expires_in":300})");
  }

  static std::string ManifestUrl(const std::string& reference) {
    return absl::StrCat(
        "https://registry.example.com/v2/library/nginx/manifests/", reference);
  }

  static std::string BlobUrl(const std::string& digest) {
    return absl::StrCat("https://registry.example.com/v2/library/nginx/blobs/",
                        digest);
  }

  absl::StatusOr<std::vector<Manifest>> Resolve(
      const std::optional<std::string>& platform) {
    auto client =
        RegistryClient::Create(TestReference(), registry_.getter(), "", "");
    if (!client.ok()) {
      return client.status();
    }

    return (*client)->ResolveManifests(platform);
  }

  void AnswerIndex() {
    registry_.AnswerManifest(
        ManifestUrl("latest"), kOciIndex,
        absl::StrCat(
            R"({"schemaVersion":2,"manifests":[)", R"({"digest":")",
            kAmd64Digest,
            R"(","platform":{"os":"linux","architecture":"amd64"}},)",
            R"({"digest":")", kArm64Digest,
            R"(","platform":{"os":"linux","architecture":"arm64","variant":"v8"}},)",
            R"({"digest":")", kAttestationDigest,
            R"(","platform":{"os":"unknown","architecture":"unknown"}}]})"));
    registry_.AnswerManifest(ManifestUrl(kAmd64Digest), kOciManifest,
                             ImageManifest("sha256:config-amd64",
                                           {"sha256:shared", "sha256:amd64"}));
    registry_.AnswerManifest(ManifestUrl(kArm64Digest), kOciManifest,
                             ImageManifest("sha256:config-arm64",
                                           {"sha256:shared", "sha256:arm64"}));
    registry_.AnswerManifest(
        ManifestUrl(kAttestationDigest), kOciManifest,
        ImageManifest("sha256:config-attestation", {"sha256:attestation"}));
  }

  FakeRegistry registry_;
};

TEST_F(ResolveTest, ResolvesAnImageManifestReferencedByItsDigest) {
  const std::string body = ImageManifest(
      "sha256:config", {"sha256:a", "sha256:b", "sha256:a", "sha256:config"});
  registry_.AnswerManifest(
      ManifestUrl("latest"),
      "application/vnd.docker.distribution.manifest.v2+json; charset=utf-8",
      body);

  auto manifests = Resolve(std::nullopt);
  ASSERT_TRUE(manifests.ok()) << manifests.status();
  ASSERT_THAT(*manifests, SizeIs(1));
  EXPECT_THAT((*manifests)[0].url,
              ::testing::MatchesRegex(ManifestUrl("sha256:[0-9a-f]{64}")));
  EXPECT_EQ((*manifests)[0].platform, "");
  EXPECT_THAT((*manifests)[0].blob_urls,
              ElementsAre(BlobUrl("sha256:config"), BlobUrl("sha256:a"),
                          BlobUrl("sha256:b")));
  EXPECT_EQ(registry_.AuthorizationOf(ManifestUrl("latest")), "Bearer abc");

  manifests = Resolve("linux/amd64");
  ASSERT_TRUE(manifests.ok()) << manifests.status();
  ASSERT_THAT(*manifests, SizeIs(1));
  EXPECT_EQ((*manifests)[0].platform, "linux/amd64");
}

TEST_F(ResolveTest, ResolvesEveryPlatformOfAnImageIndex) {
  AnswerIndex();

  auto manifests = Resolve(std::nullopt);
  ASSERT_TRUE(manifests.ok()) << manifests.status();
  ASSERT_THAT(*manifests, SizeIs(3));
  EXPECT_EQ((*manifests)[0].platform, "linux/amd64");
  EXPECT_THAT((*manifests)[0].blob_urls,
              ElementsAre(BlobUrl("sha256:config-amd64"),
                          BlobUrl("sha256:shared"), BlobUrl("sha256:amd64")));
  EXPECT_EQ((*manifests)[1].platform, "linux/arm64");
  EXPECT_EQ((*manifests)[2].platform, "unknown/unknown");
  EXPECT_THAT((*manifests)[2].blob_urls,
              ElementsAre(BlobUrl("sha256:config-attestation"),
                          BlobUrl("sha256:attestation")));
}

TEST_F(ResolveTest, ResolvesOnlyThePlatformGiven) {
  AnswerIndex();

  for (const std::string platform : {"linux/arm64", "linux/aarch64"}) {
    auto manifests = Resolve(platform);
    ASSERT_TRUE(manifests.ok()) << manifests.status();
    ASSERT_THAT(*manifests, SizeIs(1));
    EXPECT_EQ((*manifests)[0].platform, "linux/arm64");
    EXPECT_THAT((*manifests)[0].blob_urls,
                ElementsAre(BlobUrl("sha256:config-arm64"),
                            BlobUrl("sha256:shared"), BlobUrl("sha256:arm64")));
  }

  EXPECT_EQ(registry_.Count(ManifestUrl(kAmd64Digest)), 0);
}

TEST_F(ResolveTest, FailsWithoutAMatchingManifest) {
  AnswerIndex();

  auto manifests = Resolve("windows/amd64");
  EXPECT_EQ(manifests.status().code(), absl::StatusCode::kInternal);
  EXPECT_EQ(manifests.status().message(),
            "no matching manifest for platform windows/amd64");

  EXPECT_EQ(Resolve("linux-amd64").status().code(),
            absl::StatusCode::kInvalidArgument);
}

TEST_F(ResolveTest, FailsWhenTheRegistryRefusesTheManifest) {
  auto manifests = Resolve(std::nullopt);
  EXPECT_EQ(manifests.status().code(), absl::StatusCode::kInternal);
  EXPECT_THAT(std::string(manifests.status().message()),
              HasSubstr("failed to pull image manifest"));
  EXPECT_THAT(std::string(manifests.status().message()), HasSubstr("404"));

  registry_.AnswerManifest(ManifestUrl("latest"), "text/html", "{}");
  EXPECT_FALSE(Resolve(std::nullopt).ok());
}

}  // namespace
}  // namespace dragonfly::client_request::internal
