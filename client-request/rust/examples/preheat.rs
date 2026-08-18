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

//! Preheats a file or an OCI image to the seed peers via the Dragonfly.
//!
//! Usage: cargo run --example preheat --features preheat -- <scheduler-endpoint> file|image <url-or-image>

use dragonfly_client_request::{Builder, Client, PreheatImageRequest, PreheatRequest};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let args: Vec<String> = std::env::args().collect();
    if args.len() != 4 || (args[2] != "file" && args[2] != "image") {
        eprintln!(
            "usage: {} <scheduler-endpoint> file|image <url-or-image>",
            args[0]
        );
        std::process::exit(1);
    }

    let client = Builder::default()
        .scheduler_endpoint(args[1].clone())
        .build()
        .await?;

    match args[2].as_str() {
        "file" => {
            client
                .preheat(&PreheatRequest {
                    url: args[3].clone(),
                    ..Default::default()
                })
                .await?
        }
        _ => {
            client
                .preheat_image(&PreheatImageRequest {
                    image: args[3].clone(),
                    ..Default::default()
                })
                .await?
        }
    }

    Ok(())
}
