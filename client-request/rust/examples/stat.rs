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

//! Queries the distribution of an OCI image in the seed peers via the Dragonfly.
//!
//! Usage: cargo run --example stat --features preheat -- <scheduler-endpoint> <image>

use dragonfly_client_request::{Proxy, Request, StatImageRequest};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: {} <scheduler-endpoint> <image>", args[0]);
        std::process::exit(1);
    }

    let proxy = Proxy::builder()
        .scheduler_endpoint(args[1].clone())
        .build()
        .await?;

    let response = proxy
        .stat_image(&StatImageRequest {
            image: args[2].clone(),
            ..Default::default()
        })
        .await?;

    println!("image has {} layers", response.layers.len());
    for peer in response.peers.iter() {
        let finished = peer
            .cached_layers
            .iter()
            .filter(|layer| layer.is_finished)
            .count();

        println!(
            "peer {} ({}) finished {} of {} cached layers",
            peer.hostname,
            peer.ip,
            finished,
            peer.cached_layers.len()
        );
    }

    Ok(())
}
