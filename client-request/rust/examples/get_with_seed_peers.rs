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

//! Lists the seed peers from the scheduler, then downloads a file via the
//! Dragonfly from them and writes it to stdout.
//!
//! Usage: cargo run --example get_with_seed_peers -- <scheduler-endpoint> <url>

use dragonfly_client_request::{
    GetRequest, ProxyWithSeedPeers, RequestWithSeedPeers, SchedulerClient,
};
use futures::TryStreamExt;
use std::time::Duration;
use tokio::io::AsyncWriteExt;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: {} <scheduler-endpoint> <url>", args[0]);
        std::process::exit(1);
    }

    // Install the crypto provider of the clients to the seed peer proxies.
    let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();

    let scheduler_client = SchedulerClient::new(&args[1], Duration::from_secs(5)).await?;
    let seed_peers = scheduler_client.list_seed_peers().await?;
    for seed_peer in seed_peers.iter() {
        eprintln!(
            "seed peer {} {}:{} healthy: {}",
            seed_peer.host.hostname,
            seed_peer.host.ip,
            seed_peer.host.proxy_port,
            seed_peer.healthy
        );
    }

    let proxy = ProxyWithSeedPeers::builder()
        .seed_peers(seed_peers)
        .build()
        .await?;
    let response = proxy
        .get(&GetRequest {
            url: args[2].clone(),
            ..Default::default()
        })
        .await?;

    // The body is a stream of zero-copy `Bytes` chunks.
    let mut body = response.body.ok_or("missing response body")?;
    let mut stdout = tokio::io::stdout();
    while let Some(chunk) = body.try_next().await? {
        stdout.write_all(&chunk).await?;
    }
    stdout.flush().await?;

    Ok(())
}
