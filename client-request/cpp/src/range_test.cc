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

#include "src/range.h"

#include <gtest/gtest.h>

#include <limits>
#include <utility>

namespace dragonfly::client_request::internal {
namespace {

TEST(RangeTest, HttpRangeToStringFormatsTheRangeHeader) {
  const std::pair<HttpRange, const char*> test_cases[] = {
      {{0, 1}, "bytes=0-0"},
      {{2, 4}, "bytes=2-5"},
      {{600000, 448576}, "bytes=600000-1048575"},
  };

  for (const auto& [range, expected] : test_cases) {
    EXPECT_EQ(range.ToString(), expected);
  }
}

TEST(RangeTest, ParseContentRangeParsesASatisfiedRange) {
  constexpr std::int64_t kMax = std::numeric_limits<std::int64_t>::max();
  const std::pair<const char*, ContentRange> test_cases[] = {
      {"bytes 2-5/8", {{2, 4}, 8}},
      {"bytes 0-7/8", {{0, 8}, 8}},
      {"bytes 0-0/1", {{0, 1}, 1}},
      {"bytes 9223372036854775806-9223372036854775806/9223372036854775807",
       {{kMax - 1, 1}, kMax}},
  };

  for (const auto& [value, expected] : test_cases) {
    auto content_range = ParseContentRange(value);
    ASSERT_TRUE(content_range.has_value()) << value;
    EXPECT_TRUE(*content_range == expected) << value;
  }
}

TEST(RangeTest, ParseContentRangeRejectsAnInvalidRange) {
  for (const char* value :
       {"bytes 2-5/*", "bytes */8", "items 2-5/8", "bytes 2/8", "bytes x-5/8",
        "bytes 2-x/8", "bytes 2-5/x", "bytes +2-5/8", "bytes 2-5/+8",
        "bytes 2-5/8/9", "bytes 5-2/8", "bytes 2-5/5", "bytes 2-5/0",
        "bytes 2-5/9223372036854775808", " bytes 2-5/8"}) {
    EXPECT_FALSE(ParseContentRange(value).has_value()) << value;
  }
}

}  // namespace
}  // namespace dragonfly::client_request::internal
