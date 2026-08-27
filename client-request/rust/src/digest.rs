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

//! Mirrored from the dragonfly client's `dragonfly-client-util/src/digest/mod.rs`.
//! The digest extraction here decides the task id, so keep both copies in sync,
//! any divergence silently produces different task ids between the SDK and the
//! client.

use regex::Regex;
use std::fmt;
use std::str::FromStr;
use std::sync::LazyLock;

/// The separator character for digest formatting.
pub const SEPARATOR: &str = ":";

/// Regex pattern for OCI blob URLs, e.g. http(s)://<registry>/v2/<repository>/blobs/<digest>.
static BLOB_URL_REGEX: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^(.*)://(.*)/v2/(.*)/blobs/([^?]+)(?:\?.*)?$").unwrap());

/// Checks if the URL is an OCI blob URL.
pub fn is_blob_url(url: &str) -> bool {
    BLOB_URL_REGEX.is_match(url)
}

/// Regex pattern for OCI manifest URLs, e.g. http(s)://<registry>/v2/<repository>/manifests/<reference>.
static MANIFEST_URL_REGEX: LazyLock<Regex> =
    LazyLock::new(|| Regex::new(r"^(.*)://(.*)/v2/(.*)/manifests/([^?]+)(?:\?.*)?$").unwrap());

/// Checks if the URL is an OCI manifest URL whose reference is a digest with a
/// supported algorithm. A manifest URL referenced by a tag returns false, so it
/// falls back to the url based task id.
pub fn is_manifest_digest_url(url: &str) -> bool {
    Digest::extract_from_manifest_url(url).is_some()
}

/// Algorithm for generating digests.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Algorithm {
    /// CRC32 algorithm for generating digests.
    Crc32,

    /// SHA-256 algorithm for generating digests.
    Sha256,

    /// SHA-512 algorithm for generating digests.
    Sha512,
}

/// Implements the Display.
impl fmt::Display for Algorithm {
    /// Formats the value using the given formatter.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Algorithm::Crc32 => write!(f, "crc32"),
            Algorithm::Sha256 => write!(f, "sha256"),
            Algorithm::Sha512 => write!(f, "sha512"),
        }
    }
}

/// Implements the FromStr.
impl FromStr for Algorithm {
    type Err = String;

    /// Parses an algorithm string.
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        match s {
            "crc32" => Ok(Algorithm::Crc32),
            "sha256" => Ok(Algorithm::Sha256),
            "sha512" => Ok(Algorithm::Sha512),
            _ => Err(format!("invalid digest algorithm: {s}")),
        }
    }
}

/// A digest value with its associated algorithm.
pub struct Digest {
    /// The algorithm used to generate the digest.
    algorithm: Algorithm,

    /// The encoded digest value.
    encoded: String,
}

/// Implements the Digest.
impl Digest {
    /// Creates a new digest with the specified algorithm and encoded value.
    pub fn new(algorithm: Algorithm, encoded: String) -> Self {
        Self { algorithm, encoded }
    }

    /// Extracts the digest from an OCI blob URL, e.g. http(s)://<registry>/v2/<repository>/blobs/<digest>.
    pub fn extract_from_blob_url(url: &str) -> Option<Self> {
        BLOB_URL_REGEX
            .captures(url)
            .and_then(|caps| caps.get(4))
            .map(|m| m.as_str())?
            .parse()
            .ok()
    }

    /// Extracts the digest from an OCI manifest URL, e.g. http(s)://<registry>/v2/<repository>/manifests/<digest>.
    pub fn extract_from_manifest_url(url: &str) -> Option<Self> {
        MANIFEST_URL_REGEX
            .captures(url)
            .and_then(|caps| caps.get(4))
            .map(|m| m.as_str())?
            .parse()
            .ok()
    }

    /// Returns the algorithm of the digest.
    pub fn algorithm(&self) -> Algorithm {
        self.algorithm
    }

    /// Returns the encoded digest value.
    pub fn encoded(&self) -> &str {
        &self.encoded
    }
}

/// Implements the Display.
impl fmt::Display for Digest {
    /// Formats the value using the given formatter.
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{}{}{}", self.algorithm, SEPARATOR, self.encoded)
    }
}

/// Implements the FromStr.
impl FromStr for Digest {
    type Err = String;

    /// Parses a digest string.
    fn from_str(s: &str) -> Result<Self, Self::Err> {
        let (algorithm, encoded) = s
            .split_once(SEPARATOR)
            .ok_or_else(|| format!("invalid digest: {s}"))?;

        let algorithm = match algorithm {
            "crc32" => {
                if encoded.is_empty() {
                    return Err(format!("invalid crc32 digest: {s}"));
                }

                Algorithm::Crc32
            }
            "sha256" => {
                if encoded.len() != 64 {
                    return Err(format!(
                        "invalid sha256 digest length: {}, expected 64",
                        encoded.len()
                    ));
                }

                Algorithm::Sha256
            }
            "sha512" => {
                if encoded.len() != 128 {
                    return Err(format!(
                        "invalid sha512 digest length: {}, expected 128",
                        encoded.len()
                    ));
                }

                Algorithm::Sha512
            }
            _ => return Err(format!("invalid digest algorithm: {algorithm}")),
        };

        Ok(Digest::new(algorithm, encoded.to_string()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_from_blob_url() {
        let test_cases = vec![
            (
                "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
                Some((Algorithm::Sha256, "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e")),
            ),
            (
                "https://registry.example.com/v2/myorg/myrepo/blobs/sha512:94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8",
                Some((Algorithm::Sha512, "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8")),
            ),
            (
                "https://registry.io/v2/org/team/project/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
                Some((Algorithm::Sha256, "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e")),
            ),
            (
                "http://localhost:5000/v2/myrepo/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io",
                Some((Algorithm::Sha256, "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e")),
            ),
            ("http://registry.example.com/blobs/sha256:abc", None),
            ("http://registry.example.com/v2/repo/manifests/sha256:abc", None),
            ("registry.example.com/v2/repo/blobs/sha256:abc", None),
            ("http://registry.example.com/v2/blobs/sha256:abc", None),
            ("", None),
            ("not-a-url", None),
            ("http://registry.example.com/v2/repo/blobs/invalid-digest", None),
            ("http://registry.example.com/v2/repo/blobs/md5:8a04994a666b4e4b20a2fd9e5a44f44c", None),
        ];

        for (url, expected) in test_cases {
            match expected {
                Some((algorithm, encoded)) => {
                    let digest = Digest::extract_from_blob_url(url).unwrap();
                    assert_eq!(digest.algorithm(), algorithm);
                    assert_eq!(digest.encoded(), encoded);
                }
                None => assert!(Digest::extract_from_blob_url(url).is_none()),
            }
        }
    }

    #[test]
    fn test_is_manifest_digest_url() {
        let test_cases = vec![
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
                true,
            ),
            (
                "http://localhost:5000/v2/myrepo/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io",
                true,
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/latest",
                false,
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
                false,
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/sha256:abc",
                false,
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
                false,
            ),
            ("https://example.com/file.txt", false),
        ];

        for (url, expected) in test_cases {
            assert_eq!(is_manifest_digest_url(url), expected);
        }
    }

    #[test]
    fn test_extract_from_manifest_url() {
        let test_cases = vec![
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
                Some((Algorithm::Sha256, "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e")),
            ),
            (
                "http://localhost:5000/v2/myrepo/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io",
                Some((Algorithm::Sha256, "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e")),
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/latest",
                None,
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/sha256:abc",
                None,
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/manifests/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
                None,
            ),
            (
                "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
                None,
            ),
            ("https://example.com/file.txt", None),
        ];

        for (url, expected) in test_cases {
            match expected {
                Some((algorithm, encoded)) => {
                    let digest = Digest::extract_from_manifest_url(url).unwrap();
                    assert_eq!(digest.algorithm(), algorithm);
                    assert_eq!(digest.encoded(), encoded);
                }
                None => assert!(Digest::extract_from_manifest_url(url).is_none()),
            }
        }
    }

    #[test]
    fn test_algorithm_display() {
        let test_cases = vec![
            (Algorithm::Crc32, "crc32"),
            (Algorithm::Sha256, "sha256"),
            (Algorithm::Sha512, "sha512"),
        ];

        for (algorithm, expected) in test_cases {
            assert_eq!(algorithm.to_string(), expected);
        }
    }

    #[test]
    fn test_algorithm_from_str() {
        let test_cases = vec![
            ("crc32", Some(Algorithm::Crc32)),
            ("sha256", Some(Algorithm::Sha256)),
            ("sha512", Some(Algorithm::Sha512)),
            ("invalid", None),
        ];

        for (s, expected) in test_cases {
            assert_eq!(s.parse::<Algorithm>().ok(), expected);
        }
    }

    #[test]
    fn test_digest_display() {
        let test_cases = vec![
            (Algorithm::Crc32, "1475635037", "crc32:1475635037"),
            (Algorithm::Sha256, "encoded_hash", "sha256:encoded_hash"),
            (Algorithm::Sha512, "encoded_hash", "sha512:encoded_hash"),
        ];

        for (algorithm, encoded, expected) in test_cases {
            assert_eq!(
                Digest::new(algorithm, encoded.to_string()).to_string(),
                expected
            );
        }
    }
}
