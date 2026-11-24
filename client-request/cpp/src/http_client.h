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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_HTTP_CLIENT_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_HTTP_CLIENT_H_

#include <chrono>
#include <cstddef>
#include <cstdint>
#include <memory>
#include <mutex>
#include <string>
#include <vector>

#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "dragonfly/client_request/errors.h"

namespace dragonfly::client_request::internal {

/// A GET request.
struct HttpRequest {
  /// The url of the request.
  std::string url;

  /// The headers of the request.
  Header header;

  /// The proxy the request is sent through, such as "http://127.0.0.1:4001",
  /// none when empty.
  std::string proxy;

  /// The timeout of the whole request, its body included, none when zero.
  std::chrono::milliseconds timeout{0};

  /// The longest wait for the response and for every read of its body, none
  /// when zero.
  std::chrono::milliseconds read_timeout{0};

  /// Whether the TLS certificate of the server is left unverified.
  bool insecure_skip_verify = false;
};

/// A response with a streaming body. Destroying it releases the request. It
/// is not thread-safe.
class HttpResponse {
 public:
  virtual ~HttpResponse() = default;

  /// Returns the status code of the response.
  virtual int status_code() const = 0;

  /// Returns the headers of the response, their names lowercased.
  virtual const Header& header() const = 0;

  /// Returns the length of the body, -1 when the response does not tell it.
  virtual std::int64_t content_length() const = 0;

  /// Reads up to size bytes into the buffer and returns the bytes read, 0 at
  /// the end of the body.
  virtual absl::StatusOr<std::size_t> Read(char* buffer, std::size_t size) = 0;
};

/// The libcurl handles of a request, kept with their connections.
struct CurlHandles;

/// Sends GET requests, reusing its connections. It is thread-safe.
class HttpClient : public std::enable_shared_from_this<HttpClient> {
 public:
  /// Creates a client.
  static std::shared_ptr<HttpClient> Create();

  ~HttpClient();
  HttpClient(const HttpClient&) = delete;
  HttpClient& operator=(const HttpClient&) = delete;

  /// Sends the request, following its redirects, and returns the response
  /// once its headers arrived, whatever its status code.
  absl::StatusOr<std::unique_ptr<HttpResponse>> Get(const HttpRequest& request);

  /// Keeps the handles of a finished request for a later one.
  void Release(std::unique_ptr<CurlHandles> handles);

 private:
  HttpClient();

  /// Returns the handles kept last, new ones when none is fresh.
  std::unique_ptr<CurlHandles> Acquire();

  /// Protects idle_.
  std::mutex mu_;

  /// The handles of the finished requests, the ones released last at the
  /// back.
  std::vector<std::unique_ptr<CurlHandles>> idle_;
};

/// Reports whether the request failed because the proxy refused the
/// connection or is unreachable.
bool IsConnectError(const absl::Status& status);

}  // namespace dragonfly::client_request::internal

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_HTTP_CLIENT_H_
