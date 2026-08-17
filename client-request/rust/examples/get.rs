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

//! Downloads a file via the Dragonfly and writes it to stdout.
//!
//! Usage: cargo run --example get -- <scheduler-endpoint> <url>

use dragonfly_client_request::{GetRequest, Proxy, Request};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 3 {
        eprintln!("usage: {} <scheduler-endpoint> <url>", args[0]);
        std::process::exit(1);
    }

    let proxy = Proxy::builder()
        .scheduler_endpoint(args[1].clone())
        .build()
        .await?;

    let response = proxy
        .get(&GetRequest {
            url: args[2].clone(),
            ..Default::default()
        })
        .await?;

    let mut reader = response.reader.ok_or("missing response body")?;
    tokio::io::copy(&mut reader, &mut tokio::io::stdout()).await?;

    Ok(())
}
