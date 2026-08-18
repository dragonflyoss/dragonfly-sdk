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

//! Looks up the endpoints of the seed peers serving the url via the Dragonfly,
//! then downloads the file from a randomly picked endpoint and writes it to
//! stdout.
//!
//! Usage: cargo run --example get_with_endpoints -- <scheduler-endpoint> <url>

use dragonfly_client_request::{Builder, Client, GetRequest};
use futures::TryStreamExt;
use tokio::io::AsyncWriteExt;

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: {} <scheduler-endpoint> <url>", args[0]);
        std::process::exit(1);
    }

    let client = Builder::default()
        .scheduler_endpoint(args[1].clone())
        .build()
        .await?;

    let request = GetRequest {
        url: args[2].clone(),
        ..Default::default()
    };

    let endpoints = client.lookup_endpoints(&request).await?;
    let response = client.get_with_endpoints(&endpoints, &request).await?;

    // The body is a stream of zero-copy `Bytes` chunks.
    let mut body = response.body.ok_or("missing response body")?;
    let mut stdout = tokio::io::stdout();
    while let Some(chunk) = body.try_next().await? {
        stdout.write_all(&chunk).await?;
    }
    stdout.flush().await?;

    Ok(())
}
