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

#include "src/http_client_pool.h"

#include <gtest/gtest.h>

#include <chrono>

namespace dragonfly::client_request::internal {
namespace {

using std::chrono::seconds;

TEST(HttpClientPoolTest, EntryReturnsTheClientOfTheEndpoint) {
  HttpClientPool pool(128, std::chrono::minutes(30));
  auto first = pool.Entry("http://127.0.0.1:4001");
  EXPECT_EQ(pool.Entry("http://127.0.0.1:4001"), first);
  EXPECT_NE(pool.Entry("http://127.0.0.1:4002"), first);
  EXPECT_EQ(pool.size(), 2U);
}

TEST(HttpClientPoolTest, EntryDropsTheIdleClientsButNotTheActiveOnes) {
  const auto now = std::chrono::steady_clock::now();
  HttpClientPool pool(128, seconds(40));
  auto active = pool.Entry("http://127.0.0.1:4001", now);
  pool.Entry("http://127.0.0.1:4002", now);
  EXPECT_EQ(pool.size(), 2U);

  pool.Entry("http://127.0.0.1:4003", now + seconds(80));
  EXPECT_EQ(pool.size(), 2U);
  EXPECT_EQ(pool.Entry("http://127.0.0.1:4001", now + seconds(80)), active);
  EXPECT_EQ(pool.size(), 2U);
}

TEST(HttpClientPoolTest, EntryKeepsTheClientsUsedWithinTheIdleTimeout) {
  const auto now = std::chrono::steady_clock::now();
  HttpClientPool pool(128, seconds(40));
  pool.Entry("http://127.0.0.1:4001", now);
  pool.Entry("http://127.0.0.1:4002", now + seconds(30));

  pool.Entry("http://127.0.0.1:4003", now + seconds(50));
  EXPECT_EQ(pool.size(), 2U);
}

TEST(HttpClientPoolTest, EntryDropsTheClientsThatExceedTheCapacity) {
  const auto now = std::chrono::steady_clock::now();
  HttpClientPool pool(1, seconds(40));
  pool.Entry("http://127.0.0.1:4001", now);
  pool.Entry("http://127.0.0.1:4002", now);
  EXPECT_EQ(pool.size(), 2U);

  pool.Entry("http://127.0.0.1:4003", now + seconds(25));
  EXPECT_EQ(pool.size(), 1U);
}

TEST(HttpClientPoolTest, EntrySkipsACleanupTooSoonAfterTheLastOne) {
  const auto now = std::chrono::steady_clock::now();
  HttpClientPool pool(1, seconds(40));
  pool.Entry("http://127.0.0.1:4001", now);
  pool.Entry("http://127.0.0.1:4002", now);
  pool.Entry("http://127.0.0.1:4003", now + seconds(19));
  EXPECT_EQ(pool.size(), 3U);
}

}  // namespace
}  // namespace dragonfly::client_request::internal
