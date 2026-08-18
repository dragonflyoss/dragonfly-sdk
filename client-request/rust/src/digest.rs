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

use regex::Regex;
use std::sync::LazyLock;

/// Regex pattern for OCI blob URLs, e.g. http(s)://<registry>/v2/<repository>/blobs/<digest>.
static BLOB_URL_REGEX: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^(.*)://(.*)/v2/(.*)/blobs/([^?]+)(?:\?.*)?$").unwrap());

/// Checks if the URL is an OCI blob URL.
pub fn is_blob_url(url: &str) -> bool {
    BLOB_URL_REGEX.is_match(url)
}

/// Extracts the digest from an OCI blob URL and returns its encoded value,
/// validating the algorithm and the encoded length.
pub fn extract_encoded_from_blob_url(url: &str) -> Option<String> {
    let digest = BLOB_URL_REGEX
        .captures(url)
        .and_then(|caps| caps.get(4))
        .map(|m| m.as_str())?;

    let (algorithm, encoded) = digest.split_once(':')?;
    let expected_len = match algorithm {
        "crc32" => 10,
        "sha256" => 64,
        "sha512" => 128,
        _ => return None,
    };

    if encoded.len() != expected_len {
        return None;
    }

    Some(encoded.to_string())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_is_blob_url() {
        assert!(is_blob_url(
            "https://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
        ));
        assert!(is_blob_url(
            "http://registry.example.com/v2/myorg/myrepo/blobs/sha256:abcdef?ns=docker.io"
        ));
        assert!(!is_blob_url(
            "https://registry.example.com/v2/library/ubuntu/manifests/latest"
        ));
        assert!(!is_blob_url("https://example.com/file.txt"));
    }

    #[test]
    fn test_extract_encoded_from_blob_url() {
        assert_eq!(
            extract_encoded_from_blob_url(
                "https://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
            ),
            Some("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e".to_string())
        );

        // Query params are excluded from the digest.
        assert_eq!(
            extract_encoded_from_blob_url(
                "https://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io"
            ),
            Some("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e".to_string())
        );

        // Invalid algorithm.
        assert_eq!(
            extract_encoded_from_blob_url(
                "https://registry.example.com/v2/library/ubuntu/blobs/md5:abcdef"
            ),
            None
        );

        // Invalid encoded length.
        assert_eq!(
            extract_encoded_from_blob_url(
                "https://registry.example.com/v2/library/ubuntu/blobs/sha256:abcdef"
            ),
            None
        );

        // Not a blob URL.
        assert_eq!(
            extract_encoded_from_blob_url("https://example.com/file.txt"),
            None
        );
    }
}
