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

//! Cross-language consistency vectors shared with the Go SDK
//! (client-request/go). Both test suites assert the same fixed outputs, so a
//! change on either side that breaks task id compatibility fails the build.

use dragonfly_client_request::id_generator::{IDGenerator, TaskIDParameter};

#[test]
fn task_id_matches_go() {
    let generator = IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false);

    let test_cases = vec![
        (
            TaskIDParameter::URLBased {
                url: "https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar".to_string(),
                piece_length: Some(4194304),
                tag: Some("tag1".to_string()),
                application: Some("app1".to_string()),
                filtered_query_params: vec!["Expires".to_string(), "Signature".to_string()],
                revision: None,
            },
            "2a0c4c713d7f2f65f36b78b79c4b78a6bf5d5f67b76730ed13485d3271482f1c",
        ),
        (
            TaskIDParameter::URLBased {
                url: "https://example.com/file.txt".to_string(),
                piece_length: None,
                tag: None,
                application: None,
                filtered_query_params: vec![],
                revision: None,
            },
            "7fcf06e5f0b1e443065c1a563eed788eb2e168a05c6ad9c4b319f7a976322be0",
        ),
        (
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
            TaskIDParameter::URLBased {
                url: "https://example.com/file.txt?k=a b&m=x*y&n=c~d".to_string(),
                piece_length: Some(1024),
                tag: None,
                application: None,
                filtered_query_params: vec!["none".to_string()],
                revision: None,
            },
            "6196a6846023f6d3c1e4d30f6c86f3d4186e4c664a33e5692b0e04e49b26a9af",
        ),
        (
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
            TaskIDParameter::URLBased {
                url: "https://example.com/file.txt?a=1;x&b=2".to_string(),
                piece_length: None,
                tag: None,
                application: None,
                filtered_query_params: vec!["none".to_string()],
                revision: None,
            },
            "370933e9d7ab42b3ed90287213cd5f8def6091c8606da5b2dc79436061b06c34",
        ),
        (
            TaskIDParameter::URLBased {
                url: "http://www.xx.yy/path?u=f&x=y&m=z&x=s#size".to_string(),
                piece_length: None,
                tag: None,
                application: None,
                filtered_query_params: vec!["x".to_string(), "m".to_string()],
                revision: None,
            },
            "3570c3c808f06fd250a7a60634b9275d72c56edc248576508bb06264c2c65825",
        ),
        (
            TaskIDParameter::Content("This is a test file".to_string()),
            "e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249",
        ),
        (
            TaskIDParameter::BlobDigestBased(
                "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
                    .to_string(),
            ),
            "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
        ),
        (
            TaskIDParameter::ManifestDigestBased(
                "http://registry.example.com/v2/library/ubuntu/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
                    .to_string(),
            ),
            "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
        ),
        (
            TaskIDParameter::ManifestDigestBased(
                "http://localhost:5000/v2/myrepo/manifests/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e?ns=docker.io"
                    .to_string(),
            ),
            "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e",
        ),
        (
            TaskIDParameter::URLBased {
                url: "http://registry.example.com/v2/library/ubuntu/manifests/latest".to_string(),
                piece_length: None,
                tag: None,
                application: None,
                filtered_query_params: vec![],
                revision: None,
            },
            "0b6500b31c7eea2929393e154299bc81ebbb613c24fa7f5f33e893d585f4d629",
        ),
    ];

    for (parameter, expected) in test_cases {
        assert_eq!(generator.task_id(parameter).unwrap(), expected);
    }
}
