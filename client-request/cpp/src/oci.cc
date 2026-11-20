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

#include <openssl/sha.h>
#include <re2/re2.h>

#include <algorithm>
#include <chrono>
#include <utility>

#include "absl/strings/ascii.h"
#include "absl/strings/escaping.h"
#include "absl/strings/match.h"
#include "absl/strings/str_cat.h"
#include "google/protobuf/struct.pb.h"
#include "google/protobuf/util/json_util.h"
#include "src/request_internal.h"
#include "src/url.h"

namespace dragonfly::client_request::internal {
namespace {

// The longest wait for the answer of a registry and for every read of it.
constexpr std::chrono::seconds kRegistryReadTimeout(60);

constexpr char kDefaultDomain[] = "docker.io";

constexpr char kDockerManifest[] =
    "application/vnd.docker.distribution.manifest.v2+json";
constexpr char kDockerManifestList[] =
    "application/vnd.docker.distribution.manifest.list.v2+json";
constexpr char kOciManifest[] = "application/vnd.oci.image.manifest.v1+json";
constexpr char kOciIndex[] = "application/vnd.oci.image.index.v1+json";

absl::Status InvalidReference(const std::string& cause) {
  return absl::InvalidArgumentError(
      absl::StrCat("invalid image reference: ", cause));
}

bool HasUpper(absl::string_view text) {
  return std::any_of(text.begin(), text.end(),
                     [](unsigned char c) { return absl::ascii_isupper(c); });
}

// The grammar of an image reference, refer to
// https://github.com/distribution/reference/blob/main/regexp.go. A digest is
// of an algorithm go-digest registers.
struct ReferenceGrammar {
  const RE2 identifier{"[a-f0-9]{64}"};
  const RE2 domain{
      R"((?:(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))"
      R"((?:\.(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9]))*)"
      R"(|\[[a-fA-F0-9:]+\])(?::[0-9]+)?)"};
  const RE2 path{R"([a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*)"
                 R"((?:/[a-z0-9]+(?:(?:[._]|__|[-]+)[a-z0-9]+)*)*)"};
  const RE2 tag{R"([\w][\w.-]{0,127})"};
  const RE2 digest{
      "sha256:[a-f0-9]{64}|sha384:[a-f0-9]{96}|"
      "sha512:[a-f0-9]{128}"};
};

const ReferenceGrammar& Grammar() {
  static const auto* const grammar = new ReferenceGrammar();
  return *grammar;
}

// Reports whether the whole text matches the regex.
bool Matches(absl::string_view text, const RE2& regex) {
  return RE2::FullMatch(re2::StringPiece(text.data(), text.size()), regex);
}

// The tokens of a challenge are made of any visible ASCII character but the
// separators.
bool IsTokenChar(unsigned char c) {
  return c > 32 && c < 127 &&
         !absl::StrContains("\"(),/:;<=>?@[]\\{}", static_cast<char>(c));
}

void SkipSpace(absl::string_view* text) {
  text->remove_prefix(
      std::min(text->find_first_not_of(" \t\r\n"), text->size()));
}

std::string ConsumeToken(absl::string_view* text) {
  std::size_t size = 0;
  while (size < text->size() && IsTokenChar((*text)[size])) {
    ++size;
  }

  std::string token(text->substr(0, size));
  text->remove_prefix(size);
  return token;
}

// Consumes a token or a quoted string, in which a backslash escapes the next
// character. An unclosed quoted string is empty.
std::string ConsumeValue(absl::string_view* text) {
  if (text->empty() || text->front() != '"') {
    return ConsumeToken(text);
  }

  std::string value;
  for (std::size_t i = 1; i < text->size(); ++i) {
    if ((*text)[i] == '"') {
      text->remove_prefix(i + 1);
      return value;
    }

    if ((*text)[i] == '\\') {
      ++i;
      if (i == text->size()) {
        break;
      }
    }

    value.push_back((*text)[i]);
  }

  *text = {};
  return "";
}

// Parses a challenge of a Www-Authenticate header into its lowercased scheme
// and its parameters, as the challenge parser of docker distribution does.
std::pair<std::string, std::map<std::string, std::string>> ParseChallenge(
    absl::string_view text) {
  std::string scheme = ConsumeToken(&text);
  absl::AsciiStrToLower(&scheme);

  std::map<std::string, std::string> parameters;
  for (;;) {
    SkipSpace(&text);
    std::string name = ConsumeToken(&text);
    if (name.empty() || text.empty() || text.front() != '=') {
      break;
    }

    text.remove_prefix(1);
    std::string value = ConsumeValue(&text);
    if (value.empty()) {
      break;
    }

    absl::AsciiStrToLower(&name);
    parameters[name] = std::move(value);
    SkipSpace(&text);
    if (text.empty() || text.front() != ',') {
      break;
    }

    text.remove_prefix(1);
  }

  return {std::move(scheme), std::move(parameters)};
}

// Returns the basic credentials, empty unless there are a username and a
// password.
std::string BasicCredentials(const std::string& username,
                             const std::string& password) {
  if (username.empty() || password.empty()) {
    return "";
  }

  return absl::StrCat(
      "Basic ", absl::Base64Escape(absl::StrCat(username, ":", password)));
}

// Returns the failure to authenticate with the registry, keeping its code.
absl::Status AuthenticationError(const absl::Status& status) {
  return absl::Status(
      status.code(),
      absl::StrCat("failed to authenticate with registry: ", status.message()));
}

absl::Status UnexpectedStatus(const HttpResult& answer) {
  return absl::InternalError(absl::StrCat("received unexpected HTTP status ",
                                          answer.status_code, ": ",
                                          answer.body));
}

// Returns the field of a JSON object, the null value when it is absent.
const google::protobuf::Value& Field(const google::protobuf::Struct& object,
                                     const char* name) {
  auto it = object.fields().find(name);
  return it == object.fields().end()
             ? google::protobuf::Value::default_instance()
             : it->second;
}

// Returns the media type of the answer, without its parameters.
std::string MediaType(const Header& header) {
  auto it = header.find("content-type");
  if (it == header.end()) {
    return "";
  }

  std::string media_type = it->second.substr(0, it->second.find(';'));
  absl::StripAsciiWhitespace(&media_type);
  absl::AsciiStrToLower(&media_type);
  return media_type;
}

std::string Sha256Digest(const std::string& content) {
  unsigned char hash[SHA256_DIGEST_LENGTH];
  SHA256(reinterpret_cast<const unsigned char*>(content.data()), content.size(),
         hash);
  return absl::StrCat("sha256:", absl::BytesToHexString(absl::string_view(
                                     reinterpret_cast<const char*>(hash),
                                     SHA256_DIGEST_LENGTH)));
}

}  // namespace

std::string Reference::ManifestUrl() const {
  return absl::StrCat(scheme, "://", registry, "/v2/", repository,
                      "/manifests/", reference);
}

std::string Reference::BlobUrl(absl::string_view digest) const {
  return absl::StrCat(scheme, "://", registry, "/v2/", repository, "/blobs/",
                      std::string(digest));
}

std::string Reference::ToString() const {
  std::string identifier = reference.substr(0, reference.find('?'));
  return absl::StrCat(registry, "/", repository,
                      absl::StrContains(identifier, ':') ? "@" : ":",
                      identifier);
}

absl::StatusOr<Reference> ParseImage(absl::string_view image) {
  if (Matches(image, Grammar().identifier)) {
    return InvalidReference(
        absl::StrCat("invalid repository name (", std::string(image),
                     "), cannot specify 64-byte hexadecimal strings"));
  }

  // Split the domain off the name. The first component is a domain when it is
  // localhost, has a period, a colon or an uppercase letter.
  absl::string_view domain = kDefaultDomain;
  std::string remainder(image);
  if (auto slash = image.find('/'); slash != absl::string_view::npos) {
    absl::string_view first = image.substr(0, slash);
    if (first == "index.docker.io") {
      remainder = std::string(image.substr(slash + 1));
    } else if (first == "localhost" ||
               first.find_first_of(".:") != absl::string_view::npos ||
               HasUpper(first)) {
      domain = first;
      remainder = std::string(image.substr(slash + 1));
    }
  }

  if (domain == kDefaultDomain && !absl::StrContains(remainder, '/')) {
    remainder.insert(0, "library/");
  }

  absl::string_view path = remainder;
  if (absl::string_view name = path.substr(0, path.find(':')); HasUpper(name)) {
    return InvalidReference(
        absl::StrCat("invalid reference format: repository name (",
                     std::string(name), ") must be lowercase"));
  }

  std::optional<absl::string_view> digest;
  if (auto at = path.find('@'); at != absl::string_view::npos) {
    digest = path.substr(at + 1);
    path = path.substr(0, at);
  }

  std::optional<absl::string_view> tag;
  if (auto colon = path.find(':'); colon != absl::string_view::npos) {
    tag = path.substr(colon + 1);
    path = path.substr(0, colon);
  }

  const ReferenceGrammar& grammar = Grammar();
  if (!Matches(domain, grammar.domain) || !Matches(path, grammar.path) ||
      (tag && !Matches(*tag, grammar.tag)) ||
      (digest && !Matches(*digest, grammar.digest))) {
    return InvalidReference("invalid reference format");
  }

  if (path.size() > 255) {
    return InvalidReference(
        "repository name must not be more than 255 characters");
  }

  Reference reference;
  reference.scheme = "https";
  reference.registry =
      domain == kDefaultDomain ? "registry-1.docker.io" : std::string(domain);
  reference.repository = std::string(path);
  reference.reference = std::string(digest ? *digest : tag.value_or("latest"));
  return reference;
}

HttpGetter NewRegistryGetter(std::shared_ptr<HttpClient> client) {
  return [client = std::move(client)](
             const std::string& url,
             const Header& header) -> absl::StatusOr<HttpResult> {
    HttpRequest request;
    request.url = url;
    request.header = header;
    request.read_timeout = kRegistryReadTimeout;
    auto response = client->Get(request);
    if (!response.ok()) {
      return response.status();
    }

    HttpResult result;
    result.status_code = (*response)->status_code();
    result.header = (*response)->header();
    char buffer[16 * 1024];
    for (;;) {
      const auto n = (*response)->Read(buffer, sizeof(buffer));
      if (!n.ok()) {
        return n.status();
      }

      if (*n == 0) {
        return result;
      }

      result.body.append(buffer, *n);
    }
  };
}

const std::vector<std::string>& ManifestMediaTypes() {
  static const auto* const media_types = new std::vector<std::string>{
      kDockerManifest, kDockerManifestList, kOciManifest, kOciIndex};
  return *media_types;
}

RegistryClient::RegistryClient(Reference reference, HttpGetter getter,
                               std::string username, std::string password)
    : reference_(std::move(reference)),
      getter_(std::move(getter)),
      username_(std::move(username)),
      password_(std::move(password)) {}

absl::StatusOr<std::unique_ptr<RegistryClient>> RegistryClient::Create(
    Reference reference, HttpGetter getter, std::string username,
    std::string password) {
  auto ping =
      getter(absl::StrCat(reference.scheme, "://", reference.registry, "/v2/"),
             Header());
  if (!ping.ok()) {
    return AuthenticationError(ping.status());
  }

  std::unique_ptr<RegistryClient> client(
      new RegistryClient(std::move(reference), std::move(getter),
                         std::move(username), std::move(password)));
  if (ping->status_code != 401) {
    return client;
  }

  auto [first, last] = ping->header.equal_range("www-authenticate");
  for (auto it = first; it != last; ++it) {
    auto [scheme, parameters] = ParseChallenge(it->second);
    if (scheme == "bearer" && !client->bearer_) {
      client->bearer_ = std::move(parameters);
    } else if (scheme == "basic") {
      client->basic_ = true;
    }
  }

  return client;
}

absl::StatusOr<std::string> RegistryClient::Authorization() {
  std::string authorization;
  if (bearer_) {
    std::lock_guard<std::mutex> lock(mu_);
    if (absl::Now() > token_expiration_) {
      absl::Status status = FetchToken();
      if (!status.ok()) {
        return AuthenticationError(status);
      }
    }

    authorization = absl::StrCat("Bearer ", token_);
  }

  // The basic credentials win over the bearer token, as in the authorizer of
  // docker distribution.
  if (basic_) {
    authorization = BasicCredentials(username_, password_);
    if (authorization.empty()) {
      return AuthenticationError(
          absl::InternalError("no basic auth credentials"));
    }
  }

  return authorization;
}

absl::Status RegistryClient::FetchToken() {
  auto realm = bearer_->find("realm");
  if (realm == bearer_->end()) {
    return absl::InternalError("no realm specified for token auth challenge");
  }

  // Build the query sorted by name, as the token handler of docker
  // distribution does.
  Header header;
  std::string url = absl::StrCat(
      realm->second, absl::StrContains(realm->second, '?') ? "&" : "?");
  if (std::string credentials = BasicCredentials(username_, password_);
      !credentials.empty()) {
    absl::StrAppend(&url, "account=", QueryEscape(username_), "&");
    header.emplace("Authorization", std::move(credentials));
  }

  absl::StrAppend(
      &url, "scope=",
      QueryEscape(absl::StrCat("repository:", reference_.repository, ":pull")));
  if (auto service = bearer_->find("service"); service != bearer_->end()) {
    absl::StrAppend(&url, "&service=", QueryEscape(service->second));
  }

  auto answer = getter_(url, header);
  if (!answer.ok()) {
    return answer.status();
  }

  if (answer->status_code / 100 != 2) {
    return UnexpectedStatus(*answer);
  }

  google::protobuf::Struct response;
  if (!google::protobuf::util::JsonStringToMessage(answer->body, &response)
           .ok()) {
    return absl::InternalError("unable to decode token response");
  }

  std::string token = Field(response, "access_token").string_value();
  if (token.empty()) {
    token = Field(response, "token").string_value();
  }

  if (token.empty()) {
    return absl::InternalError(
        "authorization server did not include a token in the response");
  }

  absl::Time issued_at = absl::Now();
  std::string error;
  if (const std::string& value = Field(response, "issued_at").string_value();
      !value.empty() &&
      !absl::ParseTime(absl::RFC3339_full, value, &issued_at, &error)) {
    return absl::InternalError(
        absl::StrCat("unable to decode token response: ", error));
  }

  token_ = std::move(token);
  token_expiration_ =
      issued_at + absl::Seconds(std::max(
                      Field(response, "expires_in").number_value(), 60.0));
  return absl::OkStatus();
}

absl::StatusOr<std::vector<Manifest>> RegistryClient::ResolveManifests(
    const std::optional<std::string>& platform) {
  std::optional<std::string> normalized;
  if (platform) {
    auto result = NormalizePlatform(*platform);
    if (!result.ok()) {
      return result.status();
    }

    normalized = std::move(*result);
  }

  auto manifests = Resolve(reference_, normalized);
  if (!manifests.ok()) {
    return absl::Status(manifests.status().code(),
                        absl::StrCat("failed to pull image manifest: ",
                                     manifests.status().message()));
  }

  if (manifests->empty()) {
    return absl::InternalError(absl::StrCat(
        "no matching manifest for platform ", normalized.value_or("all")));
  }

  if (normalized) {
    for (auto& manifest : *manifests) {
      manifest.platform = *normalized;
    }
  }

  return manifests;
}

// NOLINTNEXTLINE(misc-no-recursion)
absl::StatusOr<std::vector<Manifest>> RegistryClient::Resolve(
    const Reference& reference, const std::optional<std::string>& platform) {
  Header header;
  for (const auto& media_type : ManifestMediaTypes()) {
    header.emplace("Accept", media_type);
  }

  auto authorization = Authorization();
  if (!authorization.ok()) {
    return authorization.status();
  }

  if (!authorization->empty()) {
    header.emplace("Authorization", *authorization);
  }

  auto answer = getter_(reference.ManifestUrl(), header);
  if (!answer.ok()) {
    return answer.status();
  }

  if (answer->status_code / 100 != 2) {
    return UnexpectedStatus(*answer);
  }

  google::protobuf::Struct document;
  if (!google::protobuf::util::JsonStringToMessage(answer->body, &document)
           .ok()) {
    return absl::InternalError("unable to decode manifest");
  }

  // Fall back to the media type the document tells when the answer has none.
  std::string media_type = MediaType(answer->header);
  if (media_type.empty()) {
    media_type = Field(document, "mediaType").string_value();
  }

  if (media_type == kDockerManifest || media_type == kOciManifest) {
    Reference digest_reference = reference;
    digest_reference.reference = Sha256Digest(answer->body);

    // A blob referenced more than once by the manifest is resolved once.
    Manifest manifest;
    manifest.url = digest_reference.ManifestUrl();
    auto add_blob = [&](const google::protobuf::Value& descriptor) {
      std::string url = reference.BlobUrl(
          Field(descriptor.struct_value(), "digest").string_value());
      if (std::find(manifest.blob_urls.begin(), manifest.blob_urls.end(),
                    url) == manifest.blob_urls.end()) {
        manifest.blob_urls.push_back(std::move(url));
      }
    };

    add_blob(Field(document, "config"));
    for (const auto& layer : Field(document, "layers").list_value().values()) {
      add_blob(layer);
    }

    return std::vector<Manifest>{std::move(manifest)};
  }

  if (media_type == kDockerManifestList || media_type == kOciIndex) {
    std::vector<Manifest> manifests;
    for (const auto& value :
         Field(document, "manifests").list_value().values()) {
      const auto& entry = value.struct_value();
      const auto& spec = Field(entry, "platform").struct_value();
      const std::string& os = Field(spec, "os").string_value();
      const std::string& architecture =
          Field(spec, "architecture").string_value();

      // An entry without an os and an architecture matches no platform.
      std::string entry_platform;
      if (!os.empty() && !architecture.empty()) {
        entry_platform =
            NormalizePlatform(absl::StrCat(os, "/", architecture)).value_or("");
      }

      if (platform && entry_platform != *platform) {
        continue;
      }

      Reference digest_reference = reference;
      digest_reference.reference = Field(entry, "digest").string_value();
      auto children = Resolve(digest_reference, platform);
      if (!children.ok()) {
        return children.status();
      }

      for (auto& child : *children) {
        if (!entry_platform.empty()) {
          child.platform = entry_platform;
        }

        manifests.push_back(std::move(child));
      }
    }

    return manifests;
  }

  return absl::InternalError(
      absl::StrCat("unsupported manifest media type: ", media_type));
}

}  // namespace dragonfly::client_request::internal
