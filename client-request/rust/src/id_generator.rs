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

use crate::digest;
use crate::errors::Error;
use crate::url::filter_query_params;
use crate::Result;
use dragonfly_api::common::v2::TaskType;
use sha2::{Digest as Sha2Digest, Sha256};

/// The parameter of the task id.
pub enum TaskIDParameter {
    /// Content uses the content to generate the task id.
    Content(String),
    /// URLBased uses the url, piece_length, tag, application and filtered_query_params to generate
    /// the task id.
    URLBased {
        url: String,
        piece_length: Option<u64>,
        tag: Option<String>,
        application: Option<String>,
        filtered_query_params: Vec<String>,
        // Revision is used to generate the task id for the artifact with the same url but
        // different revisions, such as git repository.
        revision: Option<String>,
    },
    /// BlobDigestBased will extract the digest in the oci blob url and use the digest's encoded as
    /// the task id.
    BlobDigestBased(String),
}

/// Used to generate the id for the resources.
#[derive(Debug)]
pub struct IDGenerator {
    /// The ip of the host.
    ip: String,

    /// The hostname of the host.
    hostname: String,

    /// Indicates whether the host is a seed peer.
    is_seed_peer: bool,
}

/// Implements the IDGenerator.
impl IDGenerator {
    /// Creates a new IDGenerator.
    pub fn new(ip: String, hostname: String, is_seed_peer: bool) -> Self {
        IDGenerator {
            ip,
            hostname,
            is_seed_peer,
        }
    }

    /// Generates the host id.
    #[inline]
    pub fn host_id(&self) -> String {
        if self.is_seed_peer {
            return format!("{}-{}-{}", self.ip, self.hostname, "seed");
        }

        format!("{}-{}", self.ip, self.hostname)
    }

    /// Generates the task id.
    #[inline]
    pub fn task_id(&self, parameter: TaskIDParameter) -> Result<String> {
        match parameter {
            TaskIDParameter::Content(content) => {
                Ok(hex::encode(Sha256::digest(content.as_bytes())))
            }
            TaskIDParameter::URLBased {
                url,
                piece_length,
                tag,
                application,
                filtered_query_params,
                revision,
            } => {
                // Canonicalize the url, identical to the scheduler's task id generation.
                let final_url = filter_query_params(&url, &filtered_query_params)?;

                // Initialize the hasher.
                let mut hasher = Sha256::new();

                // Add the url to generate the task id.
                hasher.update(final_url);

                // Add the tag to generate the task id.
                if let Some(tag) = tag {
                    hasher.update(tag);
                }

                // Add the application to generate the task id.
                if let Some(application) = application {
                    hasher.update(application);
                }

                // Add the revision to generate the task id for the artifact with the same url but
                // different revisions, such as git repository.
                if let Some(revision) = revision {
                    hasher.update(revision);
                }

                // Add the piece length to generate the task id.
                if let Some(piece_length) = piece_length {
                    hasher.update(piece_length.to_string());
                }

                hasher.update(TaskType::Standard.as_str_name().as_bytes());

                // Generate the task id.
                Ok(hex::encode(hasher.finalize()))
            }
            TaskIDParameter::BlobDigestBased(url) => digest::extract_encoded_from_blob_url(&url)
                .ok_or_else(|| Error::InvalidArgument(format!("invalid blob url: {url}"))),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn should_generate_host_id() {
        let test_cases = vec![
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                "127.0.0.1-localhost",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), true),
                "127.0.0.1-localhost-seed",
            ),
        ];

        for (generator, expected) in test_cases {
            assert_eq!(generator.host_id(), expected);
        }
    }

    #[test]
    fn should_generate_task_id() {
        let test_cases = vec![
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com".to_string(),
                    piece_length: Some(1024_u64),
                    tag: Some("foo".to_string()),
                    application: Some("bar".to_string()),
                    filtered_query_params: vec![],
                    revision: Some("v1.0".to_string()),
                },
                "5844f27a257287e9b734256bb25603d8005422ced8c0377f15063ec11963b25f",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com".to_string(),
                    piece_length: None,
                    tag: Some("foo".to_string()),
                    application: Some("bar".to_string()),
                    filtered_query_params: vec![],
                    revision: None,
                },
                "06408fbf247ddaca478f8cb9565fe5591c28efd0994b8fea80a6a87d3203c5ca",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com".to_string(),
                    piece_length: None,
                    tag: Some("foo".to_string()),
                    application: None,
                    filtered_query_params: vec![],
                    revision: None,
                },
                "3c3f230ef9f191dd2821510346a7bc138e4894bee9aee184ba250a3040701d2a",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com".to_string(),
                    piece_length: None,
                    tag: None,
                    application: Some("bar".to_string()),
                    filtered_query_params: vec![],
                    revision: None,
                },
                "c9f9261b7305c24371244f9f149f5d4589ed601348fdf22d7f6f4b10658fdba2",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com".to_string(),
                    piece_length: Some(1024_u64),
                    tag: None,
                    application: None,
                    filtered_query_params: vec![],
                    revision: None,
                },
                "9f7c9aafbc6f30f8f41a96ca77eeae80c5b60964b3034b0ee43ccf7b2f9e52b8",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com?foo=foo&bar=bar".to_string(),
                    piece_length: None,
                    tag: None,
                    application: None,
                    filtered_query_params: vec!["foo".to_string(), "bar".to_string()],
                    revision: None,
                },
                "457b4328cde278e422c9e243f7bfd1e97f511fec43a80f535cf6b0ef6b086776",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com/file.txt?z=9&b=2&a=1".to_string(),
                    piece_length: None,
                    tag: Some("foo".to_string()),
                    application: Some("bar".to_string()),
                    filtered_query_params: vec!["z".to_string()],
                    revision: None,
                },
                "8b3f6e9b9b8fe20903bced565cfd1d0aaef354a4c17573f0c2c1979210443f9d",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com/file.txt?b=2&a=1&b=1".to_string(),
                    piece_length: None,
                    tag: None,
                    application: None,
                    filtered_query_params: vec!["c".to_string()],
                    revision: None,
                },
                "7c8801d0596be5e8f9449d5c4af23866c72fe5205119c0e5912981f3b16a37aa",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com/file.txt?k=a b&m=x*y&n=c~d".to_string(),
                    piece_length: Some(1024_u64),
                    tag: None,
                    application: None,
                    filtered_query_params: vec!["none".to_string()],
                    revision: None,
                },
                "6196a6846023f6d3c1e4d30f6c86f3d4186e4c664a33e5692b0e04e49b26a9af",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com/file.txt?a=1&b=2".to_string(),
                    piece_length: None,
                    tag: Some("foo".to_string()),
                    application: None,
                    filtered_query_params: vec!["a".to_string(), "b".to_string()],
                    revision: None,
                },
                "c8f4b41117329d54af920010394f6f607bac707e933ab2f18d372e3dd4c7fcb3",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com/file.txt?b=2&a=1".to_string(),
                    piece_length: None,
                    tag: None,
                    application: None,
                    filtered_query_params: vec![],
                    revision: None,
                },
                "980ee327518ccc5a7c30703e1a2232e8ba9047b39431f940636c85b6146f8b9a",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::URLBased {
                    url: "https://example.com".to_string(),
                    piece_length: None,
                    tag: None,
                    application: None,
                    filtered_query_params: vec![],
                    revision: Some("v1.0".to_string()),
                },
                "b171331534b80e0bf91da38ebbfcdbf4d177898f4b9beac44f14733e3f004d4e",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::Content("This is a test file".to_string()),
                "e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::BlobDigestBased(
                    "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
                        .to_string(),
                ),
                "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::BlobDigestBased(
                    "https://registry.example.com/v2/myorg/myrepo/blobs/sha512:94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8"
                        .to_string(),
                ),
                "94381a28e8c039fedfa78de025158a068226c3ccd041b22c2c8e73fc993584e9b167d9ae32bc8b372c66701c808ab134e0768c8f16b9a3e61eec1ccf8faa9db8",
            ),
            (
                IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false),
                TaskIDParameter::BlobDigestBased(
                    "http://localhost:5000/v2/myrepo/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io"
                        .to_string(),
                ),
                "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
            ),
        ];

        for (generator, parameter, expected_id) in test_cases {
            let task_id = generator.task_id(parameter).unwrap();
            assert_eq!(task_id, expected_id);
        }

        let generator = IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false);
        for url in [
            "https://example.com/file.txt",
            "http://registry.example.com/v2/library/ubuntu/blobs/sha256:abc",
            "http://registry.example.com/v2/library/ubuntu/blobs/md5:8a04994a666b4e4b20a2fd9e5a44f44c",
        ] {
            assert!(generator
                .task_id(TaskIDParameter::BlobDigestBased(url.to_string()))
                .is_err());
        }
    }
}
