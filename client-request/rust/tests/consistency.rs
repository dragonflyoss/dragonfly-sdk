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
//! change on either side that breaks hashring or task id compatibility fails
//! the build.

use dragonfly_client_util::hashring::VNodeHashRing;
use dragonfly_client_util::id_generator::{IDGenerator, TaskIDParameter};
use siphasher::sip::SipHasher;
use std::hash::Hasher;

/// Computes the hash of the vnode with the Rust `Hash` encoding: the replica
/// id as 8 native-endian bytes, the name bytes and the 0xff terminator.
fn vnode_hash(id: usize, name: &str) -> u64 {
    let mut hasher = SipHasher::new();
    hasher.write_usize(id);
    hasher.write(name.as_bytes());
    hasher.write_u8(0xff);
    hasher.finish()
}

/// Computes the hash of the key with the Rust `Hash` encoding: the key bytes
/// and the 0xff terminator.
fn key_hash(key: &str) -> u64 {
    let mut hasher = SipHasher::new();
    hasher.write(key.as_bytes());
    hasher.write_u8(0xff);
    hasher.finish()
}

#[test]
fn test_siphash_vectors() {
    assert_eq!(vnode_hash(0, "seed-peer-1"), 0x1e5a582b8d945969);
    assert_eq!(vnode_hash(1, "seed-peer-1"), 0xb5db98265419376c);
    assert_eq!(vnode_hash(0, "seed-peer-2"), 0xf36f748c486b09ef);
    assert_eq!(vnode_hash(511, "seed-peer-3"), 0xf0eda07426c5cac1);

    assert_eq!(
        key_hash("b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"),
        0xaa86f08bc4878a68
    );
    assert_eq!(key_hash("test-task"), 0xa78dd821b4023714);
    assert_eq!(key_hash(""), 0x20dd4eb33d9590f2);
}

#[test]
fn test_hashring_vectors() {
    let mut ring = VNodeHashRing::new(3);
    for name in ["seed-peer-1", "seed-peer-2", "seed-peer-3"] {
        ring.add(name.to_string());
    }

    let select = |key: &str, replicas: usize| -> Vec<String> {
        ring.get_with_replicas(&key.to_string(), replicas)
            .unwrap()
            .iter()
            .map(|v| v.to_string())
            .collect()
    };

    assert_eq!(
        select("task-a", 2),
        ["seed-peer-3|2", "seed-peer-1|2", "seed-peer-3|0"]
    );
    assert_eq!(
        select("task-b", 2),
        ["seed-peer-2|0", "seed-peer-2|1", "seed-peer-1|0"]
    );
    assert_eq!(
        select("task-c", 2),
        ["seed-peer-2|1", "seed-peer-1|0", "seed-peer-3|2"]
    );

    // Replicas larger than the ring shrink to the entire ring, starting
    // clockwise from the key's position.
    assert_eq!(
        select("task-a", 100),
        [
            "seed-peer-3|2",
            "seed-peer-1|2",
            "seed-peer-3|0",
            "seed-peer-1|1",
            "seed-peer-2|2",
            "seed-peer-3|1",
            "seed-peer-2|0",
            "seed-peer-2|1",
            "seed-peer-1|0"
        ]
    );
}

#[test]
fn test_task_id_vectors() {
    let generator = IDGenerator::new("127.0.0.1".to_string(), "localhost".to_string(), false);

    assert_eq!(
        generator
            .task_id(TaskIDParameter::URLBased {
                url: "https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar".to_string(),
                piece_length: Some(4194304),
                tag: Some("tag1".to_string()),
                application: Some("app1".to_string()),
                filtered_query_params: vec!["Expires".to_string(), "Signature".to_string()],
                revision: None,
            })
            .unwrap(),
        "2a0c4c713d7f2f65f36b78b79c4b78a6bf5d5f67b76730ed13485d3271482f1c"
    );

    assert_eq!(
        generator
            .task_id(TaskIDParameter::URLBased {
                url: "https://example.com/file.txt".to_string(),
                piece_length: None,
                tag: None,
                application: None,
                filtered_query_params: vec![],
                revision: None,
            })
            .unwrap(),
        "7fcf06e5f0b1e443065c1a563eed788eb2e168a05c6ad9c4b319f7a976322be0"
    );

    assert_eq!(
        generator
            .task_id(TaskIDParameter::Content("This is a test file".to_string()))
            .unwrap(),
        "e2d0fe1585a63ec6009c8016ff8dda8b17719a637405a4e23c0ff81339148249"
    );

    assert_eq!(
        generator
            .task_id(TaskIDParameter::BlobDigestBased(
                "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
                    .to_string(),
            ))
            .unwrap(),
        "b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e"
    );
}
