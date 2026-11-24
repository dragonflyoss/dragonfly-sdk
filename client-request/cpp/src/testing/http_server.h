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

#ifndef DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_HTTP_SERVER_H_
#define DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_HTTP_SERVER_H_

#include <arpa/inet.h>
#include <netinet/in.h>
#include <poll.h>
#include <sys/socket.h>
#include <unistd.h>

#include <algorithm>
#include <chrono>
#include <condition_variable>
#include <cstddef>
#include <cstdio>
#include <cstdlib>
#include <functional>
#include <mutex>
#include <optional>
#include <string>
#include <thread>
#include <utility>
#include <vector>

#include "absl/strings/ascii.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/str_split.h"
#include "absl/strings/string_view.h"
#include "dragonfly/client_request/errors.h"

namespace dragonfly::client_request::internal::testing {
inline std::string HeaderValue(const Header& header, const std::string& name) {
  const auto it = header.find(name);
  return it == header.end() ? "" : it->second;
}

struct TestRequest {
  std::string method;
  std::string target;
  Header header;
};

struct TestResponse {
  int status_code = 200;
  Header header;
  std::string body;

  std::optional<std::size_t> content_length;

  bool no_content_length = false;

  std::optional<std::size_t> send_body_bytes;

  bool refuse = false;

  std::chrono::milliseconds header_delay{0};

  std::optional<std::size_t> stall_after;
  std::chrono::milliseconds stall{0};
};

class HttpServer {
 public:
  using Handler = std::function<TestResponse(const TestRequest&)>;

  explicit HttpServer(Handler handler) : handler_(std::move(handler)) {
    listen_fd_ = socket(AF_INET, SOCK_STREAM, 0);
    int reuse = 1;
    setsockopt(listen_fd_, SOL_SOCKET, SO_REUSEADDR, &reuse, sizeof(reuse));
    sockaddr_in address{};
    address.sin_family = AF_INET;
    address.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
    if (bind(listen_fd_, reinterpret_cast<sockaddr*>(&address),
             sizeof(address)) != 0) {
      std::perror("bind");
      std::abort();
    }

    listen(listen_fd_, 64);
    socklen_t size = sizeof(address);
    getsockname(listen_fd_, reinterpret_cast<sockaddr*>(&address), &size);
    port_ = ntohs(address.sin_port);
    accept_thread_ = std::thread([this] { Accept(); });
  }

  ~HttpServer() {
    {
      std::scoped_lock lock(mu_);
      stopping_ = true;
      for (int fd : connection_fds_) {
        shutdown(fd, SHUT_RDWR);
      }
    }

    stop_cv_.notify_all();
    accept_thread_.join();
    for (auto& thread : connection_threads_) {
      thread.join();
    }

    for (int fd : connection_fds_) {
      close(fd);
    }

    close(listen_fd_);
  }

  int port() const { return port_; }

  std::string endpoint() const {
    return absl::StrCat("http://127.0.0.1:", port_);
  }

  int requests() const {
    std::scoped_lock lock(mu_);
    return static_cast<int>(requests_.size());
  }

  std::vector<TestRequest> received() const {
    std::scoped_lock lock(mu_);
    return requests_;
  }

  int connections() const {
    std::scoped_lock lock(mu_);
    return static_cast<int>(connection_fds_.size());
  }

 private:
  void Accept() {
    for (;;) {
      pollfd poll_fd{listen_fd_, POLLIN, 0};
      const int ready = poll(&poll_fd, 1, 20);
      std::scoped_lock lock(mu_);
      if (stopping_) {
        return;
      }

      if (ready <= 0) {
        continue;
      }

      const int fd = accept(listen_fd_, nullptr, nullptr);
      if (fd < 0) {
        continue;
      }

#ifdef SO_NOSIGPIPE
      int no_sigpipe = 1;
      setsockopt(fd, SOL_SOCKET, SO_NOSIGPIPE, &no_sigpipe, sizeof(no_sigpipe));
#endif
      connection_fds_.push_back(fd);
      connection_threads_.emplace_back([this, fd] { Serve(fd); });
    }
  }

  void Serve(int fd) {
    std::string buffer;
    for (;;) {
      std::size_t end = buffer.find("\r\n\r\n");
      while (end == std::string::npos) {
        char chunk[4096];
        const ssize_t n = recv(fd, chunk, sizeof(chunk), 0);
        if (n <= 0) {
          shutdown(fd, SHUT_RDWR);
          return;
        }

        buffer.append(chunk, static_cast<std::size_t>(n));
        end = buffer.find("\r\n\r\n");
      }

      TestRequest request;
      const std::vector<std::string> lines =
          absl::StrSplit(buffer.substr(0, end), "\r\n");
      const std::vector<std::string> request_line =
          absl::StrSplit(lines[0], ' ');
      request.method = request_line[0];
      request.target = request_line.size() > 1 ? request_line[1] : "";
      for (std::size_t i = 1; i < lines.size(); ++i) {
        const auto colon = lines[i].find(':');
        if (colon != std::string::npos) {
          request.header.emplace(
              absl::AsciiStrToLower(lines[i].substr(0, colon)),
              std::string(
                  absl::StripAsciiWhitespace(lines[i].substr(colon + 1))));
        }
      }
      buffer.erase(0, end + 4);

      {
        std::scoped_lock lock(mu_);
        requests_.push_back(request);
      }

      const TestResponse response = handler_(request);
      if (response.refuse || !Wait(response.header_delay)) {
        shutdown(fd, SHUT_RDWR);
        return;
      }

      const std::size_t declared =
          response.content_length.value_or(response.body.size());
      const std::size_t sent =
          std::min(response.send_body_bytes.value_or(response.body.size()),
                   response.body.size());
      const bool closes = response.no_content_length || sent < declared;
      std::string head =
          absl::StrCat("HTTP/1.1 ", response.status_code, " Status\r\n");
      for (const auto& header : response.header) {
        absl::StrAppend(&head, header.first, ": ", header.second, "\r\n");
      }

      if (!response.no_content_length) {
        absl::StrAppend(&head, "Content-Length: ", declared, "\r\n");
      }

      absl::StrAppend(&head, closes ? "Connection: close\r\n\r\n" : "\r\n");
      const std::size_t first =
          std::min(response.stall_after.value_or(sent), sent);
      if (!Send(fd, head) || !Send(fd, response.body.substr(0, first)) ||
          (response.stall_after && !Wait(response.stall)) ||
          !Send(fd, response.body.substr(first, sent - first)) || closes) {
        shutdown(fd, SHUT_RDWR);
        return;
      }
    }
  }

  bool Wait(std::chrono::milliseconds delay) {
    std::unique_lock<std::mutex> lock(mu_);
    return !stop_cv_.wait_for(lock, delay, [this] { return stopping_; });
  }

  static bool Send(int fd, absl::string_view data) {
    while (!data.empty()) {
#ifdef MSG_NOSIGNAL
      const ssize_t n = send(fd, data.data(), data.size(), MSG_NOSIGNAL);
#else
      const ssize_t n = send(fd, data.data(), data.size(), 0);
#endif
      if (n <= 0) {
        return false;
      }

      data.remove_prefix(static_cast<std::size_t>(n));
    }

    return true;
  }

  Handler handler_;
  int listen_fd_ = -1;
  int port_ = 0;
  std::thread accept_thread_;
  mutable std::mutex mu_;
  std::condition_variable stop_cv_;
  bool stopping_ = false;
  std::vector<int> connection_fds_;
  std::vector<std::thread> connection_threads_;
  std::vector<TestRequest> requests_;
};

}  // namespace dragonfly::client_request::internal::testing

#endif  // DRAGONFLY_CLIENT_REQUEST_SRC_TESTING_HTTP_SERVER_H_
