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

//! The `Range` and `Content-Range` of a GET, refer to RFC 9110 Section 14.

use std::fmt;
use std::ops::RangeInclusive;
use std::str::FromStr;

/// The byte range of the `Range` header, the bytes from first to last,
/// inclusive.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct Range(pub(crate) RangeInclusive<u64>);

/// Implements Display as the `Range` header value.
impl fmt::Display for Range {
    /// Formats the range as `bytes=first-last`.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "bytes={}-{}", self.0.start(), self.0.end())
    }
}

/// The byte range of the `Content-Range` header.
#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct ContentRange {
    /// The bytes the body holds, inclusive.
    pub(crate) range: RangeInclusive<u64>,

    /// The length of the whole representation.
    pub(crate) complete_length: u64,
}

/// Implements FromStr for the `Content-Range` header value.
impl FromStr for ContentRange {
    type Err = String;

    /// Parses `bytes first-last/complete-length` of digits alone, rejecting a
    /// `*` length and invalid ranges, refer to RFC 9110 Section 14.4.
    fn from_str(value: &str) -> Result<Self, Self::Err> {
        value
            .strip_prefix("bytes ")
            .filter(|range_resp| {
                range_resp
                    .bytes()
                    .all(|byte| byte.is_ascii_digit() || matches!(byte, b'-' | b'/'))
            })
            .and_then(|range_resp| {
                let (incl_range, complete_length) = range_resp.split_once('/')?;
                let (first, last) = incl_range.split_once('-')?;
                let first = first.parse().ok()?;
                let last = last.parse().ok()?;
                let complete_length = complete_length.parse().ok()?;

                (first <= last && last < complete_length).then_some(ContentRange {
                    range: first..=last,
                    complete_length,
                })
            })
            .ok_or_else(|| format!("invalid Content-Range {value}"))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn range_to_string_formats_the_range_header() {
        let test_cases = vec![
            (Range(0..=0), "bytes=0-0"),
            (Range(2..=5), "bytes=2-5"),
            (Range(600000..=1048575), "bytes=600000-1048575"),
        ];

        for (range, expected) in test_cases {
            assert_eq!(range.to_string(), expected);
        }
    }

    #[test]
    fn content_range_from_str_parses_only_a_valid_satisfied_range() {
        let test_cases = vec![
            ("bytes 2-5/8", Some((2..=5, 8))),
            ("bytes 0-7/8", Some((0..=7, 8))),
            ("bytes 0-0/1", Some((0..=0, 1))),
            (
                "bytes 18446744073709551614-18446744073709551614/18446744073709551615",
                Some((u64::MAX - 1..=u64::MAX - 1, u64::MAX)),
            ),
            ("bytes 2-5/*", None),
            ("bytes */8", None),
            ("items 2-5/8", None),
            ("bytes 2/8", None),
            ("bytes x-5/8", None),
            ("bytes 2-x/8", None),
            ("bytes 2-5/x", None),
            ("bytes +2-5/8", None),
            ("bytes 2-5/+8", None),
            ("bytes 2-5/8/9", None),
            ("bytes 5-2/8", None),
            ("bytes 2-5/5", None),
            ("bytes 2-5/0", None),
            ("bytes 2-5/18446744073709551616", None),
        ];

        for (header, expected) in test_cases {
            let content_range = header
                .parse::<ContentRange>()
                .ok()
                .map(|content_range| (content_range.range, content_range.complete_length));
            assert_eq!(content_range, expected, "header: {header}");
        }
    }
}
