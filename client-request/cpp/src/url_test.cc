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

#include "src/url.h"

#include <gtest/gtest.h>

namespace dragonfly::client_request::internal {
namespace {

struct TestCase {
  std::string url;
  std::vector<std::string> filtered_query_params;
  std::string expected;
};

void ExpectFiltered(const std::vector<TestCase>& test_cases) {
  for (const auto& test_case : test_cases) {
    auto url =
        FilterQueryParams(test_case.url, test_case.filtered_query_params);
    ASSERT_TRUE(url.ok()) << test_case.url << ": " << url.status();
    EXPECT_EQ(*url, test_case.expected) << test_case.url;
  }
}

TEST(UrlTest, FilterQueryParamsFiltersAndSortsTheQuery) {
  ExpectFiltered({
      {"https://example.com/file.txt?z=9&b=2&a=1",
       {"z"},
       "https://example.com/file.txt?a=1&b=2"},
      {"https://example.com/file.txt?b=2&a=1&b=1",
       {"c"},
       "https://example.com/file.txt?a=1&b=2&b=1"},
      {"https://example.com?foo=foo", {"foo"}, "https://example.com"},
      {"https://example.com?foo=foo&bar=bar",
       {"foo"},
       "https://example.com?bar=bar"},
      {"https://example.com?foo=foo#size", {"foo"}, "https://example.com#size"},
      {"https://user:pass@[::1]:8080?foo=foo&bar=bar",
       {"foo"},
       "https://user:pass@[::1]:8080?bar=bar"},
      {"https://example.com/?foo=foo", {"foo"}, "https://example.com/"},
      {"https://example.com/?foo=foo&bar=bar",
       {"foo"},
       "https://example.com/?bar=bar"},
      {"https://example.com/file.txt?k=a b&m=x*y&n=c~d",
       {"none"},
       "https://example.com/file.txt?k=a+b&m=x%2Ay&n=c~d"},
      {"http://www.xx.yy/path?u=f&x=y&m=z&x=s#size",
       {"x", "m"},
       "http://www.xx.yy/path?u=f#size"},
      {"http://www.xx.yy/path?u=f&x=y&m=z&x=s#size",
       {},
       "http://www.xx.yy/path?u=f&x=y&m=z&x=s#size"},
      {"https://example.com/file.txt?a=1;x&b=2",
       {"none"},
       "https://example.com/file.txt?b=2"},
  });
}

TEST(UrlTest, FilterQueryParamsEscapesTheQueryAsGo) {
  ExpectFiltered({
      {"https://example.com/?k=a b", {"none"}, "https://example.com/?k=a+b"},
      {"https://example.com/?k=x*y", {"none"}, "https://example.com/?k=x%2Ay"},
      {"https://example.com/?k=c~d", {"none"}, "https://example.com/?k=c~d"},
      {"https://example.com/?k=1%2B1",
       {"none"},
       "https://example.com/?k=1%2B1"},
      {"https://example.com/?k=中",
       {"none"},
       "https://example.com/?k=%E4%B8%AD"},
      {"http://example.com/a?k=a+b&k2=a%2Bb&=v&k3&k4=&&k5=a=b",
       {"none"},
       "http://example.com/a?=v&k=a+b&k2=a%2Bb&k3=&k4=&k5=a%3Db"},
      {"http://example.com/a?k='q'&m=\"d\"&n=<>&%78=1",
       {"x"},
       "http://example.com/a?k=%27q%27&m=%22d%22&n=%3C%3E"},
      {"http://example.com/a?k=%FF&m=%D6%D0%CE%C4.txt&n=%E4%B8%AD&o=%E4%B8&p=%"
       "zz&q=%",
       {"none"},
       "http://example.com/a?k=%EF%BF%BD&m=%EF%BF%BD%EF%BF%BD%EF%BF%BD%EF%BF%BD"
       ".txt&n=%E4%B8%AD&o=%EF%BF%BD&p=%25zz&q=%25"},
  });
}

TEST(UrlTest, FilterQueryParamsCanonicalizesTheUrlAsRust) {
  ExpectFiltered({
      {"HTTPS://Example.COM/File.txt?b=1",
       {"none"},
       "https://example.com/File.txt?b=1"},
      {"http://example.com:80/file.txt?b=1",
       {"none"},
       "http://example.com/file.txt?b=1"},
      {"https://example.com:443/file.txt?b=1",
       {"none"},
       "https://example.com/file.txt?b=1"},
      {"https://example.com:80/file.txt?b=1",
       {"none"},
       "https://example.com:80/file.txt?b=1"},
      {"http://example.com:/file.txt?b=1",
       {"none"},
       "http://example.com/file.txt?b=1"},
      {"http://example.com:0443/file.txt?b=1",
       {"none"},
       "http://example.com:443/file.txt?b=1"},
      {"https://[2001:db8::1]:443/p?b=1",
       {"none"},
       "https://[2001:db8::1]/p?b=1"},
      {"https://USER:Pass@Example.com/p?b=1",
       {"none"},
       "https://USER:Pass@example.com/p?b=1"},
      {"https://example.com/a b/文件.txt?b=1",
       {"none"},
       "https://example.com/a%20b/%E6%96%87%E4%BB%B6.txt?b=1"},
      {"https://example.com/a%20b/%e6%96%87.txt?b=1",
       {"none"},
       "https://example.com/a%20b/%e6%96%87.txt?b=1"},
      {"https://example.com/a\"b<c>d`e{f}g|h^i[j]k?b=1",
       {"none"},
       "https://example.com/a%22b%3Cc%3Ed%60e%7Bf%7Dg|h^i[j]k?b=1"},
      {"https://example.com/path#frag ment\"<>`{}",
       {"none"},
       "https://example.com/path#frag%20ment%22%3C%3E%60{}"},
      {"https://example.com/path?b=1#",
       {"none"},
       "https://example.com/path?b=1#"},
      {"https://example.com/path?#frag",
       {"none"},
       "https://example.com/path#frag"},
      {"https://example.com/path?", {"none"}, "https://example.com/path"},
      {"https://example.com?", {"none"}, "https://example.com"},
      {"http://example.com/a?b=1&a=2#f?x=1",
       {"none"},
       "http://example.com/a?a=2&b=1#f?x=1"},
      {"s3://Bucket/Key/a b?b=1", {"none"}, "s3://Bucket/Key/a%20b?b=1"},
      {"s3://Bucket?b=1", {"none"}, "s3://Bucket?b=1"},
      {"s3://bucket:80/key?b=1", {"none"}, "s3://bucket:80/key?b=1"},
      {"s3://bucket:/key?b=1", {"none"}, "s3://bucket/key?b=1"},
      {"hdfs://NameNode:9000/user/a?b=1",
       {"none"},
       "hdfs://NameNode:9000/user/a?b=1"},
  });
}

TEST(UrlTest, FilterQueryParamsResolvesTheDotSegmentsAsRust) {
  ExpectFiltered({
      {"https://example.com/a/../b?x=1", {"none"}, "https://example.com/b?x=1"},
      {"https://example.com/a/./b?x=1",
       {"none"},
       "https://example.com/a/b?x=1"},
      {"https://example.com/a/b/..?x=1",
       {"none"},
       "https://example.com/a/?x=1"},
      {"https://example.com/a/b/.?x=1",
       {"none"},
       "https://example.com/a/b/?x=1"},
      {"https://example.com/../a?x=1", {"none"}, "https://example.com/a?x=1"},
      {"https://example.com/a/%2e%2e/b?x=1",
       {"none"},
       "https://example.com/b?x=1"},
      {"https://example.com/a/%2E/b?x=1",
       {"none"},
       "https://example.com/a/b?x=1"},
      {"https://example.com/a/.%2e/b?x=1",
       {"none"},
       "https://example.com/b?x=1"},
      {"https://example.com/a//b?x=1",
       {"none"},
       "https://example.com/a//b?x=1"},
      {"https://example.com/a/..b/c?x=1",
       {"none"},
       "https://example.com/a/..b/c?x=1"},
      {"https://example.com/a/.../c?x=1",
       {"none"},
       "https://example.com/a/.../c?x=1"},
      {"s3://Bucket/key/../other?x=1", {"none"}, "s3://Bucket/other?x=1"},
      {"file:///tmp/a/../b?x=1", {"none"}, "file:///tmp/b?x=1"},
      {"mailto:user@example.com?x=1", {"none"}, "mailto:user@example.com?x=1"},
  });
}

TEST(UrlTest, FilterQueryParamsIgnoresTheSpacesAroundAndTheNewlinesWithin) {
  ExpectFiltered({
      {"  https://example.com/a?x=1  ", {"none"}, "https://example.com/a?x=1"},
      {"https://exam\tple.com/a\nb?x=1\r\n",
       {"none"},
       "https://example.com/ab?x=1"},
      {"https://example.com/a\x7f"
       "b?x=1",
       {"none"},
       "https://example.com/a%7Fb?x=1"},
  });
}

TEST(UrlTest, ParseReturnsTheCanonicalUrl) {
  auto url = Url::Parse("HTTPS://User@Example.com:8443/a b/../c?k=a b'#f g");
  ASSERT_TRUE(url.ok()) << url.status();
  EXPECT_TRUE(url->IsHttp());
  EXPECT_EQ(url->ToString(),
            "https://User@example.com:8443/c?k=a%20b%27#f%20g");

  url = Url::Parse("http:example.com");
  ASSERT_TRUE(url.ok()) << url.status();
  EXPECT_TRUE(url->IsHttp());
  EXPECT_EQ(url->ToString(), "http://example.com/");

  url = Url::Parse("s3://bucket/key");
  ASSERT_TRUE(url.ok()) << url.status();
  EXPECT_FALSE(url->IsHttp());
  EXPECT_EQ(url->ToString(), "s3://bucket/key");

  EXPECT_EQ(Url::Parse("example.com/file.txt").status().code(),
            absl::StatusCode::kInvalidArgument);
}

TEST(UrlTest, FilterQueryParamsCanonicalizesTheHostAsRust) {
  ExpectFiltered({
      {"https://127.1/a?x=1", {"none"}, "https://127.0.0.1/a?x=1"},
      {"https://0x7f.0.0.1/a?x=1", {"none"}, "https://127.0.0.1/a?x=1"},
      {"https://[0:0:0:0:0:0:0:1]/a?x=1", {"none"}, "https://[::1]/a?x=1"},
      {"https://ex%41mple.com/a?x=1", {"none"}, "https://example.com/a?x=1"},
      {"https://\xe4\xbe\x8b\xe3\x81\x88.jp/a?x=1",
       {"none"},
       "https://xn--r8jz45g.jp/a?x=1"},
      {"https://us er:pa ss@example.com/a?x=1",
       {"none"},
       "https://us%20er:pa%20ss@example.com/a?x=1"},
      {R"(https://example.com/a\b\..\c?x=1)",
       {"none"},
       "https://example.com/a/c?x=1"},
      {"ftp://EXAMPLE.com:21/a?x=1", {"none"}, "ftp://example.com/a?x=1"},
  });
}

TEST(UrlTest, FilterQueryParamsFailsOnAnInvalidUrl) {
  for (const char* url :
       {":error_url", "example.com/file.txt", "1http://example.com/",
        "http://:80/file.txt", "http://user@:80/file.txt", "http://",
        "http://example.com:99999/", "http://example.com:abc/",
        "http://exa mple.com/", "http://a|b/"}) {
    EXPECT_EQ(FilterQueryParams(url, {"x"}).status().code(),
              absl::StatusCode::kInvalidArgument)
        << url;
  }
}

}  // namespace
}  // namespace dragonfly::client_request::internal
