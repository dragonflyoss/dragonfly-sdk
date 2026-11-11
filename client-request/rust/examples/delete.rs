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

//! Deletes a preheated file or an OCI image from the seed peers via the Dragonfly.
//! A file is deleted from the replicas of the task by default or from all seed
//! peers with the `all_seed_peers` scope, an image is deleted from all healthy
//! seed peers.
//!
//! Usage: cargo run --example delete --features preheat -- <scheduler-endpoint> file|image <url-or-image> [default|all_seed_peers]

use dragonfly_client_request::{DeleteImageRequest, DeleteRequest, Proxy, Request, Scope};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if !(4..=5).contains(&args.len()) || (args[2] != "file" && args[2] != "image") {
        eprintln!(
            "usage: {} <scheduler-endpoint> file|image <url-or-image> [default|all_seed_peers]",
            args[0]
        );
        std::process::exit(1);
    }

    // Address the replicas of the task unless the scope is given, the scope only
    // applies to a file.
    let scope = match args.get(4).map(String::as_str) {
        None | Some("default") => Scope::Default,
        Some("all_seed_peers") => Scope::AllSeedPeers,
        Some(scope) => return Err(format!("invalid scope {scope:?}").into()),
    };

    let proxy = Proxy::builder()
        .scheduler_endpoint(args[1].clone())
        .build()
        .await?;

    match args[2].as_str() {
        "file" => {
            proxy
                .delete(&DeleteRequest {
                    url: args[3].clone(),
                    scope,
                    ..Default::default()
                })
                .await?
        }
        "image" => {
            let response = proxy
                .delete_image(&DeleteImageRequest {
                    image: args[3].clone(),
                    ..Default::default()
                })
                .await?;

            for peer in response.peers.iter() {
                println!(
                    "peer {} ({}) deleted {} tasks and retained {} tasks",
                    peer.hostname,
                    peer.ip,
                    peer.deleted_task_ids.len(),
                    peer.retained_task_ids.len()
                );
            }
        }
        _ => unreachable!(),
    }

    Ok(())
}
