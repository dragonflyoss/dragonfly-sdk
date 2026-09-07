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

//! Downloads a file via the Dragonfly with a transient failure retried on
//! another seed peer, at most three times with an exponential backoff between
//! the retries, writing the content to stdout. The default is one retry at
//! once when not set.
//!
//! Usage: cargo run --example retry -- <scheduler-endpoint> <url>

use dragonfly_client_request::{ExponentialBuilder, GetRequest, Proxy, Request};
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

    let proxy = Proxy::builder()
        .scheduler_endpoint(args[1].clone())
        .max_retries(3)
        .backoff(
            ExponentialBuilder::new()
                .with_min_delay(Duration::from_millis(100))
                .with_max_delay(Duration::from_secs(1))
                .with_jitter(),
        )
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
