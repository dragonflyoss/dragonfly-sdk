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

#include "dragonfly/client_request/errors.h"

#include <gmock/gmock.h>
#include <gtest/gtest.h>

#include "src/errors_internal.h"

namespace dragonfly::client_request {
namespace {

using ::testing::ElementsAre;
using ::testing::Pair;

TEST(ErrorsTest, AnswerErrorCarriesTheDetail) {
  ErrorDetail detail;
  detail.type = ErrorType::kBackend;
  detail.status_code = 404;
  detail.header = {{"content-type", "text/plain"}, {"x-a", "1"}, {"x-a", "2"}};
  detail.message = "not\n\nfound";

  absl::Status status = internal::AnswerError(detail);
  EXPECT_EQ(status.code(), absl::StatusCode::kNotFound);

  auto got = GetErrorDetail(status);
  ASSERT_TRUE(got.has_value());
  EXPECT_EQ(got->type, ErrorType::kBackend);
  EXPECT_EQ(got->status_code, 404);
  EXPECT_THAT(got->header, ElementsAre(Pair("content-type", "text/plain"),
                                       Pair("x-a", "1"), Pair("x-a", "2")));
  EXPECT_EQ(got->message, "not\n\nfound");
}

TEST(ErrorsTest, GetErrorDetailIsEmptyWithoutAnAnswer) {
  EXPECT_FALSE(GetErrorDetail(absl::OkStatus()).has_value());
  EXPECT_FALSE(GetErrorDetail(absl::InternalError("boom")).has_value());
}

TEST(ErrorsTest, IsRetryableMarksOnlyTransientFailures) {
  auto answer = [](ErrorType type, int status_code) {
    ErrorDetail detail;
    detail.type = type;
    detail.status_code = status_code;
    return internal::AnswerError(detail);
  };

  const std::pair<absl::Status, bool> test_cases[] = {
      {absl::DeadlineExceededError("timeout"), true},
      {absl::InternalError("boom"), true},
      {absl::UnavailableError("status"), true},
      {absl::UnknownError("status"), true},
      {absl::AbortedError("status"), true},
      {absl::ResourceExhaustedError("status"), true},
      {absl::InvalidArgumentError("bad"), false},
      {absl::NotFoundError("status"), false},
      {absl::PermissionDeniedError("status"), false},
      {absl::UnauthenticatedError("status"), false},
      {answer(ErrorType::kDfdaemon, 500), true},
      {answer(ErrorType::kDfdaemon, 507), true},
      {answer(ErrorType::kDfdaemon, 422), false},
      {answer(ErrorType::kDfdaemon, 400), false},
      {answer(ErrorType::kProxy, 502), true},
      {answer(ErrorType::kProxy, 503), true},
      {answer(ErrorType::kProxy, 408), true},
      {answer(ErrorType::kProxy, 429), true},
      {answer(ErrorType::kProxy, 401), false},
      {answer(ErrorType::kProxy, 403), false},
      {answer(ErrorType::kProxy, 416), false},
      {answer(ErrorType::kBackend, 500), true},
      {answer(ErrorType::kBackend, 404), false},
  };

  for (const auto& [status, expected] : test_cases) {
    EXPECT_EQ(internal::IsRetryable(status), expected) << status;
  }
}

}  // namespace
}  // namespace dragonfly::client_request
