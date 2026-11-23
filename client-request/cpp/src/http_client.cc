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

#include "src/http_client.h"

#include <curl/curl.h>

#include <algorithm>
#include <cerrno>
#include <climits>
#include <cstring>
#include <iterator>
#include <utility>

#include "absl/strings/ascii.h"
#include "absl/strings/cord.h"
#include "absl/strings/match.h"
#include "absl/strings/str_cat.h"
#include "absl/strings/string_view.h"

namespace dragonfly::client_request::internal {

struct CurlHandles {
  CurlHandles() : multi(curl_multi_init()), easy(curl_easy_init()) {}

  ~CurlHandles() {
    curl_easy_cleanup(easy);
    curl_multi_cleanup(multi);
  }

  CurlHandles(const CurlHandles&) = delete;
  CurlHandles& operator=(const CurlHandles&) = delete;

  // The handle that drives the transfer and keeps its connections.
  CURLM* const multi;

  // The handle of the transfer.
  CURL* const easy;

  // When the last request released the handles.
  std::chrono::steady_clock::time_point released_at;
};

namespace {

// The payload of a failure to connect to the proxy.
constexpr char kConnectErrorUrl[] =
    "type.dragonfly.io/client_request.ConnectError";

// The maximum amount of time a connect waits to complete.
constexpr long kConnectTimeoutMs = 30 * 1000;  // NOLINT(google-runtime-int)

// The keep alive interval for TCP connection.
constexpr long kKeepAliveIntervalSeconds = 60;  // NOLINT(google-runtime-int)

// The redirects a request follows.
constexpr long kMaxRedirects = 10;  // NOLINT(google-runtime-int)

// The idle handles a client keeps for later requests, and for how long.
constexpr std::size_t kMaxIdleHandles = 1024;
constexpr std::chrono::seconds kIdleTimeout(90);

// The longest the transfer waits for activity before checking the time.
constexpr std::int64_t kMaxPollMs = 1000;

void InitCurl() {
  static std::once_flag once;
  std::call_once(once, [] { curl_global_init(CURL_GLOBAL_DEFAULT); });
}

// The response of a transfer driven on demand: libcurl hands the body to the
// Read in progress, and pauses while the bytes it received early are unread.
class CurlResponse : public HttpResponse {
 public:
  CurlResponse(std::shared_ptr<HttpClient> client,
               std::unique_ptr<CurlHandles> handles)
      : client_(std::move(client)), handles_(std::move(handles)) {}

  ~CurlResponse() override;
  CurlResponse(const CurlResponse&) = delete;
  CurlResponse& operator=(const CurlResponse&) = delete;

  // Starts the request and waits for the headers of the response.
  absl::Status Start(const HttpRequest& request);

  int status_code() const override { return status_code_; }

  const Header& header() const override { return header_; }

  std::int64_t content_length() const override;

  absl::StatusOr<std::size_t> Read(char* buffer, std::size_t size) override;

 private:
  // Sets the options of the transfer of the request.
  void Configure(const HttpRequest& request);

  // Drives the transfer until the predicate holds, the transfer ends or the
  // read timeout passes.
  template <typename Predicate>
  absl::Status Drive(Predicate done);

  // Returns the failure that ended the transfer.
  absl::Status TransferError() const;

  static std::size_t OnHeader(char* data, std::size_t size, std::size_t count,
                              void* userdata);
  static std::size_t OnBody(char* data, std::size_t size, std::size_t count,
                            void* userdata);

  const std::shared_ptr<HttpClient> client_;
  std::unique_ptr<CurlHandles> handles_;
  curl_slist* request_header_ = nullptr;
  char error_[CURL_ERROR_SIZE] = "";
  std::chrono::milliseconds read_timeout_{0};

  int status_code_ = 0;
  Header header_;
  bool header_done_ = false;

  // Whether the transfer ended, and how.
  bool done_ = false;
  CURLcode result_ = CURLE_OK;

  // The buffer of the Read in progress and the bytes written into it.
  char* buffer_ = nullptr;
  std::size_t buffer_size_ = 0;
  std::size_t buffer_offset_ = 0;

  // The bytes received that did not fit the buffer, the transfer pausing
  // until they are read.
  std::string spill_;
  std::size_t spill_offset_ = 0;
  bool paused_ = false;
};

CurlResponse::~CurlResponse() {
  // Removing a transfer that has not ended closes its connection, so the
  // handles are reusable either way.
  curl_multi_remove_handle(handles_->multi, handles_->easy);
  curl_easy_setopt(handles_->easy, CURLOPT_ERRORBUFFER, nullptr);
  curl_slist_free_all(request_header_);
  client_->Release(std::move(handles_));
}

absl::Status CurlResponse::Start(const HttpRequest& request) {
  Configure(request);
  if (CURLMcode code = curl_multi_add_handle(handles_->multi, handles_->easy);
      code != CURLM_OK) {
    return absl::InternalError(curl_multi_strerror(code));
  }

  if (auto status = Drive([this] { return header_done_; }); !status.ok()) {
    return status;
  }

  return header_done_ ? absl::OkStatus() : TransferError();
}

std::int64_t CurlResponse::content_length() const {
  curl_off_t length = -1;
  curl_easy_getinfo(handles_->easy, CURLINFO_CONTENT_LENGTH_DOWNLOAD_T,
                    &length);
  return length;
}

absl::StatusOr<std::size_t> CurlResponse::Read(char* buffer, std::size_t size) {
  if (size == 0) {
    return 0;
  }

  // Hand out the bytes received before this read first.
  if (spill_offset_ < spill_.size()) {
    const std::size_t n = std::min(size, spill_.size() - spill_offset_);
    std::memcpy(buffer, spill_.data() + spill_offset_, n);
    spill_offset_ += n;
    if (spill_offset_ == spill_.size()) {
      spill_.clear();
      spill_offset_ = 0;
    }

    return n;
  }

  buffer_ = buffer;
  buffer_size_ = size;
  buffer_offset_ = 0;
  if (paused_) {
    paused_ = false;
    curl_easy_pause(handles_->easy, CURLPAUSE_CONT);
  }

  absl::Status status = Drive([this] { return buffer_offset_ > 0; });
  buffer_ = nullptr;
  if (buffer_offset_ > 0) {
    return buffer_offset_;
  }

  if (!status.ok()) {
    return status;
  }

  if (result_ != CURLE_OK) {
    return TransferError();
  }

  return 0;
}

void CurlResponse::Configure(const HttpRequest& request) {
  read_timeout_ = request.read_timeout;

  // A header without a value ends with a semicolon, or libcurl leaves it out.
  for (const auto& [name, value] : request.header) {
    request_header_ = curl_slist_append(
        request_header_, (value.empty() ? absl::StrCat(name, ";")
                                        : absl::StrCat(name, ": ", value))
                             .c_str());
  }

  CURL* easy = handles_->easy;
  curl_easy_reset(easy);
  curl_easy_setopt(easy, CURLOPT_URL, request.url.c_str());
  curl_easy_setopt(easy, CURLOPT_HTTPGET, 1L);
  curl_easy_setopt(easy, CURLOPT_NOSIGNAL, 1L);
  curl_easy_setopt(easy, CURLOPT_FOLLOWLOCATION, 1L);
  curl_easy_setopt(easy, CURLOPT_MAXREDIRS, kMaxRedirects);
  curl_easy_setopt(easy, CURLOPT_HTTPHEADER, request_header_);
  curl_easy_setopt(easy, CURLOPT_CONNECTTIMEOUT_MS, kConnectTimeoutMs);
  curl_easy_setopt(easy, CURLOPT_TCP_KEEPALIVE, 1L);
  curl_easy_setopt(easy, CURLOPT_TCP_KEEPIDLE, kKeepAliveIntervalSeconds);
  curl_easy_setopt(easy, CURLOPT_TCP_KEEPINTVL, kKeepAliveIntervalSeconds);
  curl_easy_setopt(easy, CURLOPT_ERRORBUFFER, error_);
  curl_easy_setopt(easy, CURLOPT_HEADERFUNCTION, &CurlResponse::OnHeader);
  curl_easy_setopt(easy, CURLOPT_HEADERDATA, this);
  curl_easy_setopt(easy, CURLOPT_WRITEFUNCTION, &CurlResponse::OnBody);
  curl_easy_setopt(easy, CURLOPT_WRITEDATA, this);
  if (request.timeout.count() > 0) {
    curl_easy_setopt(easy, CURLOPT_TIMEOUT_MS,
                     static_cast<long>(std::min<std::int64_t>(
                         request.timeout.count(), INT_MAX)));
  }

  // Send every request through the proxy, whatever the environment excludes,
  // and keep the answer of its CONNECT out of the response.
  if (!request.proxy.empty()) {
    curl_easy_setopt(easy, CURLOPT_PROXY, request.proxy.c_str());
    curl_easy_setopt(easy, CURLOPT_NOPROXY, "");
    curl_easy_setopt(easy, CURLOPT_SUPPRESS_CONNECT_HEADERS, 1L);
  }

  if (request.insecure_skip_verify) {
    curl_easy_setopt(easy, CURLOPT_SSL_VERIFYPEER, 0L);
    curl_easy_setopt(easy, CURLOPT_SSL_VERIFYHOST, 0L);
  }
}

template <typename Predicate>
absl::Status CurlResponse::Drive(Predicate done) {
  const auto start = std::chrono::steady_clock::now();
  while (!done() && !done_) {
    int running = 0;
    if (CURLMcode code = curl_multi_perform(handles_->multi, &running);
        code != CURLM_OK) {
      return absl::InternalError(curl_multi_strerror(code));
    }

    int pending = 0;
    while (CURLMsg* message = curl_multi_info_read(handles_->multi, &pending)) {
      if (message->msg == CURLMSG_DONE) {
        done_ = true;
        result_ = message->data.result;
      }
    }

    if (done() || done_) {
      break;
    }

    // Wait for activity, no longer than the read timeout leaves.
    std::int64_t wait_ms = kMaxPollMs;
    if (read_timeout_.count() > 0) {
      const auto waited = std::chrono::duration_cast<std::chrono::milliseconds>(
          std::chrono::steady_clock::now() - start);
      if (waited >= read_timeout_) {
        return absl::DeadlineExceededError(
            absl::StrCat("no response within the read timeout of ",
                         read_timeout_.count(), "ms"));
      }

      wait_ms =
          std::min<std::int64_t>(wait_ms, (read_timeout_ - waited).count());
    }

    if (CURLMcode code = curl_multi_poll(handles_->multi, nullptr, 0,
                                         static_cast<int>(wait_ms), nullptr);
        code != CURLM_OK) {
      return absl::InternalError(curl_multi_strerror(code));
    }
  }

  return absl::OkStatus();
}

absl::Status CurlResponse::TransferError() const {
  const std::string message =
      error_[0] != '\0' ? error_ : curl_easy_strerror(result_);
  if (result_ == CURLE_OPERATION_TIMEDOUT) {
    return absl::DeadlineExceededError(message);
  }

  // Mark the failure to connect that ejects a proxy.
  absl::Status status = absl::InternalError(message);
  long os_errno = 0;  // NOLINT(google-runtime-int)
  curl_easy_getinfo(handles_->easy, CURLINFO_OS_ERRNO, &os_errno);
  if (result_ == CURLE_COULDNT_CONNECT &&
      (os_errno == ECONNREFUSED || os_errno == EHOSTUNREACH ||
       os_errno == ENETUNREACH)) {
    status.SetPayload(kConnectErrorUrl, absl::Cord());
  }

  return status;
}

std::size_t CurlResponse::OnHeader(char* data, std::size_t size,
                                   std::size_t count, void* userdata) {
  auto* response = static_cast<CurlResponse*>(userdata);
  absl::string_view line(data, size * count);

  // A status line starts the headers of a response, an interim one included.
  if (absl::StartsWith(line, "HTTP/")) {
    response->header_.clear();
    return size * count;
  }

  // An empty line ends the headers, those of the response unless it is
  // interim or a redirect libcurl follows.
  line = absl::StripAsciiWhitespace(line);
  if (line.empty()) {
    long code = 0;  // NOLINT(google-runtime-int)
    curl_easy_getinfo(response->handles_->easy, CURLINFO_RESPONSE_CODE, &code);
    const bool redirect =
        code / 100 == 3 && response->header_.count("location") > 0;
    response->status_code_ = static_cast<int>(code);
    response->header_done_ = code >= 200 && !redirect;
    return size * count;
  }

  if (const auto colon = line.find(':'); colon != absl::string_view::npos) {
    response->header_.emplace(
        absl::AsciiStrToLower(line.substr(0, colon)),
        std::string(absl::StripAsciiWhitespace(line.substr(colon + 1))));
  }

  return size * count;
}

std::size_t CurlResponse::OnBody(char* data, std::size_t size,
                                 std::size_t count, void* userdata) {
  auto* response = static_cast<CurlResponse*>(userdata);

  // Pause the transfer until the bytes received earlier are read.
  if (!response->spill_.empty()) {
    response->paused_ = true;
    return CURL_WRITEFUNC_PAUSE;
  }

  const std::size_t n =
      std::min(size * count, response->buffer_size_ - response->buffer_offset_);
  if (response->buffer_ != nullptr && n > 0) {
    std::memcpy(response->buffer_ + response->buffer_offset_, data, n);
    response->buffer_offset_ += n;
    response->spill_.assign(data + n, (size * count) - n);
  } else {
    response->spill_.assign(data, size * count);
  }

  return size * count;
}

}  // namespace

HttpClient::HttpClient() = default;

HttpClient::~HttpClient() = default;

std::shared_ptr<HttpClient> HttpClient::Create() {
  InitCurl();
  return std::shared_ptr<HttpClient>(new HttpClient());
}

absl::StatusOr<std::unique_ptr<HttpResponse>> HttpClient::Get(
    const HttpRequest& request) {
  auto handles = Acquire();
  if (handles->multi == nullptr || handles->easy == nullptr) {
    return absl::InternalError("failed to initialize the http client");
  }

  auto response =
      std::make_unique<CurlResponse>(shared_from_this(), std::move(handles));
  if (auto status = response->Start(request); !status.ok()) {
    return status;
  }

  return std::unique_ptr<HttpResponse>(std::move(response));
}

void HttpClient::Release(std::unique_ptr<CurlHandles> handles) {
  handles->released_at = std::chrono::steady_clock::now();
  std::scoped_lock lock(mu_);
  if (idle_.size() < kMaxIdleHandles) {
    idle_.push_back(std::move(handles));
  }
}

std::unique_ptr<CurlHandles> HttpClient::Acquire() {
  // The handles are ordered by their release, so the stale ones are at the
  // front. They are closed once the lock is released.
  std::vector<std::unique_ptr<CurlHandles>> stale;
  {
    std::scoped_lock lock(mu_);
    const auto now = std::chrono::steady_clock::now();
    const auto fresh =
        std::find_if(idle_.begin(), idle_.end(), [now](const auto& handles) {
          return now - handles->released_at < kIdleTimeout;
        });
    std::move(idle_.begin(), fresh, std::back_inserter(stale));
    idle_.erase(idle_.begin(), fresh);
    if (!idle_.empty()) {
      auto handles = std::move(idle_.back());
      idle_.pop_back();
      return handles;
    }
  }

  return std::make_unique<CurlHandles>();
}

bool IsConnectError(const absl::Status& status) {
  return status.GetPayload(kConnectErrorUrl).has_value();
}

}  // namespace dragonfly::client_request::internal
