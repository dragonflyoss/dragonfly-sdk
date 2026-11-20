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

#include "src/get.h"

#include <algorithm>
#include <random>
#include <thread>
#include <utility>

#include "absl/strings/str_cat.h"
#include "src/errors_internal.h"
#include "src/range.h"
#include "src/request_internal.h"
#include "src/url.h"

namespace dragonfly::client_request::internal {
namespace {

// Returns the bytes the response carries: the Content-Range of a 206, the
// whole Content-Length of a 200, nullopt otherwise.
std::optional<ContentRange> ContentRangeOf(const HttpResponse& response) {
  if (response.status_code() == 206) {
    const auto it = response.header().find("content-range");
    return it == response.header().end() ? std::nullopt
                                         : ParseContentRange(it->second);
  }

  const std::int64_t length = response.content_length();
  if (response.status_code() == 200 && length > 0) {
    return ContentRange{HttpRange{0, length}, length};
  }

  return std::nullopt;
}

// The streaming body of a response that sends its request again on the next
// seed peer for the bytes left when it breaks off.
//
// A streaming body has already handed out the bytes it read, so the request
// is sent again within the same retries, its Range narrowed to the bytes
// left:
//
//     attempt 1 ──▶ B ──▶ 206 bytes 0-14/15 ──▶ read ──▶ broke off at byte 6
//     attempt 2 ──▶ A ──▶ Range: bytes=6-14 ──▶ 206 bytes 6-14/15 ──▶ EOF
//
// That needs the first response to tell its bytes, by the Content-Range of a
// single range or the Content-Length of a whole body. A response sent again
// is matched by its Content-Range alone, which must name exactly the bytes
// left of the same size.
class ResumableBody : public Body {
 public:
  ResumableBody(std::shared_ptr<Sender> sender, HttpRequest request,
                Attempts attempts, std::unique_ptr<HttpResponse> response)
      : sender_(std::move(sender)),
        request_(std::move(request)),
        attempts_(std::move(attempts)),
        response_(std::move(response)),
        total_(ContentRangeOf(*response_)) {}

  absl::StatusOr<std::size_t> Read(char* buffer, std::size_t size) override {
    if (size == 0) {
      return 0;
    }

    while (!end_) {
      auto n = response_->Read(buffer, size);
      if (n.ok() && *n > 0) {
        offset_ += static_cast<std::int64_t>(*n);
        return n;
      }

      // Tell a body read to its end from one that broke off.
      const std::optional<ContentRange> remaining = Remaining();
      if (n.ok() && !remaining) {
        End(absl::OkStatus());
      } else if (auto status = Reopen(remaining, BrokeOff(n, remaining));
                 !status.ok()) {
        End(std::move(status));
      }
    }

    if (!end_->ok()) {
      return *end_;
    }

    return 0;
  }

 private:
  // Returns the failure of a read that did not end the body.
  static absl::Status BrokeOff(const absl::StatusOr<std::size_t>& n,
                               const std::optional<ContentRange>& remaining) {
    if (n.ok()) {
      return absl::InternalError(absl::StrCat("response body ended without ",
                                              remaining->range.ToString()));
    }

    if (absl::IsDeadlineExceeded(n.status())) {
      return n.status();
    }

    return absl::InternalError(
        absl::StrCat("failed to read response body: ", n.status().message()));
  }

  // Ends the body with the status, OK at its end, releasing the response.
  void End(absl::Status status) {
    response_.reset();
    end_ = std::move(status);
  }

  // Sends the request again for the remaining bytes on the next endpoints
  // within the retries left, failing with the status when no byte or retry
  // is left.
  absl::Status Reopen(const std::optional<ContentRange>& remaining,
                      const absl::Status& status) {
    if (!remaining) {
      return status;
    }

    const auto delay = attempts_.NextDelay();
    if (!delay) {
      return status;
    }

    // Release the response that broke off before sending the request again.
    response_.reset();
    std::this_thread::sleep_for(*delay);

    // Narrow the range of the request to the remaining bytes.
    SetHeader(&request_.header, "Range", remaining->range.ToString());

    // Send the request again until a response resumes the body, a response
    // of other bytes failing like an unreachable seed peer.
    auto response =
        attempts_.Run([&](const std::string& endpoint)
                          -> absl::StatusOr<std::unique_ptr<HttpResponse>> {
          auto attempt = sender_->SendTo(endpoint, request_);
          if (attempt.ok() && ContentRangeOf(**attempt) != remaining) {
            return absl::InternalError(
                "response sent again does not resume the body");
          }

          return attempt;
        });
    if (!response.ok()) {
      return response.status();
    }

    response_ = std::move(*response);
    return absl::OkStatus();
  }

  // Returns the bytes left of the ones the first response carries, nullopt
  // when it does not tell them or none is left.
  std::optional<ContentRange> Remaining() const {
    if (!total_ || offset_ >= total_->range.length) {
      return std::nullopt;
    }

    ContentRange remaining = *total_;
    remaining.range.start += offset_;
    remaining.range.length -= offset_;
    return remaining;
  }

  const std::shared_ptr<Sender> sender_;
  HttpRequest request_;
  Attempts attempts_;
  std::unique_ptr<HttpResponse> response_;

  // The bytes the first response carries and the bytes read so far.
  const std::optional<ContentRange> total_;
  std::int64_t offset_ = 0;

  // The status that ended the body, OK at its end, unset until then.
  std::optional<absl::Status> end_;
};

}  // namespace

Attempts::Attempts(const RetryPolicy& retry, std::vector<std::string> endpoints)
    : endpoints_(std::move(endpoints)),
      retries_(retry.max_retries()),
      delays_(retry.backoff()) {}

absl::StatusOr<Attempts> Attempts::Create(const RetryPolicy& retry,
                                          std::vector<std::string> endpoints) {
  if (endpoints.empty()) {
    return absl::InvalidArgumentError("no endpoints to send request");
  }

  thread_local std::mt19937_64 generator(std::random_device{}());
  std::shuffle(endpoints.begin(), endpoints.end(), generator);
  return Attempts(retry, std::move(endpoints));
}

absl::StatusOr<std::unique_ptr<HttpResponse>> Attempts::Run(const Send& send) {
  for (;;) {
    auto response = send(endpoints_[next_++ % endpoints_.size()]);
    if (response.ok() || !IsRetryable(response.status())) {
      return response;
    }

    const auto delay = NextDelay();
    if (!delay) {
      return response;
    }

    std::this_thread::sleep_for(*delay);
  }
}

std::optional<std::chrono::milliseconds> Attempts::NextDelay() {
  if (retries_ == 0) {
    return std::nullopt;
  }

  --retries_;
  return delays_.Next();
}

absl::StatusOr<HttpRequest> NewProxyRequest(
    const GetRequest& request, std::chrono::milliseconds read_timeout) {
  auto url = Url::Parse(request.url);
  if (!url.ok()) {
    return url.status();
  }

  auto header = ProxyHeaders(request);
  if (!header.ok()) {
    return header.status();
  }

  // The seed peer proxy answers an https request with its own certificate.
  HttpRequest http_request;
  http_request.url = url->ToString();
  http_request.header = std::move(*header);
  http_request.timeout = request.timeout;
  http_request.read_timeout = read_timeout;
  http_request.insecure_skip_verify = true;
  return http_request;
}

absl::StatusOr<std::unique_ptr<HttpResponse>> SendGet(
    HttpClient& client, const std::string& endpoint,
    const HttpRequest& request) {
  HttpRequest attempt = request;
  attempt.proxy = endpoint;
  auto response = client.Get(attempt);
  if (!response.ok()) {
    return response;
  }

  const int status_code = (*response)->status_code();
  if (status_code >= 200 && status_code < 300) {
    return response;
  }

  // Convert a non-2xx answer into an error by its X-Dragonfly-Error-Type
  // header, its body the message.
  ErrorDetail detail;
  detail.status_code = status_code;
  detail.header = (*response)->header();
  char buffer[4096];
  for (;;) {
    const auto n = (*response)->Read(buffer, sizeof(buffer));
    if (!n.ok() || *n == 0) {
      break;
    }

    detail.message.append(buffer, *n);
  }

  const auto it = detail.header.find("x-dragonfly-error-type");
  const std::string error_type = it == detail.header.end() ? "" : it->second;
  if (error_type == "backend") {
    detail.type = ErrorType::kBackend;
  } else if (error_type == "dfdaemon") {
    detail.type = ErrorType::kDfdaemon;
  } else if (error_type.empty()) {
    detail.message =
        absl::StrCat("unexpected status code from proxy: ", status_code);
  } else if (error_type != "proxy") {
    detail.message =
        absl::StrCat("unknown error type from proxy: ", error_type);
  }

  return AnswerError(detail);
}

ProxyGetter::ProxyGetter(std::shared_ptr<Sender> sender, RetryPolicy retry,
                         std::chrono::milliseconds read_timeout)
    : sender_(std::move(sender)), retry_(retry), read_timeout_(read_timeout) {}

absl::StatusOr<GetResponse> ProxyGetter::Get(
    const GetRequest& request, std::vector<std::string> endpoints) const {
  auto http_request = NewProxyRequest(request, read_timeout_);
  if (!http_request.ok()) {
    return http_request.status();
  }

  auto attempts = Attempts::Create(retry_, std::move(endpoints));
  if (!attempts.ok()) {
    return attempts.status();
  }

  auto response = attempts->Run([&](const std::string& endpoint) {
    return sender_->SendTo(endpoint, *http_request);
  });
  if (!response.ok()) {
    return response.status();
  }

  // Send the request again on the next seed peer for the bytes left when the
  // body breaks off.
  GetResponse get_response;
  get_response.success = true;
  get_response.status_code = (*response)->status_code();
  get_response.header = (*response)->header();
  get_response.body = std::make_unique<ResumableBody>(
      sender_, std::move(*http_request), std::move(*attempts),
      std::move(*response));
  return get_response;
}

absl::StatusOr<GetResponse> ProxyGetter::GetInto(
    const GetRequest& request, std::vector<std::string> endpoints,
    std::ostream& output) const {
  auto response = Get(request, std::move(endpoints));
  if (!response.ok()) {
    return response;
  }

  // Drain the response body into the output.
  std::vector<char> buffer(64 * 1024);
  for (;;) {
    const auto n = response->body->Read(buffer.data(), buffer.size());
    if (!n.ok()) {
      return n.status();
    }

    if (*n == 0) {
      break;
    }

    if (!output.write(buffer.data(), static_cast<std::streamsize>(*n))) {
      return absl::InternalError("failed to write response body");
    }
  }

  response->body.reset();
  return response;
}

}  // namespace dragonfly::client_request::internal
