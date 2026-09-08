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

//! Drives the Rust client-request SDK for the e2e tests. It is the Rust
//! counterpart of the Go driver and speaks the same command line and JSON
//! output, so the e2e suite runs the same specs against both SDKs.
//!
//! The SDK builds its HTTP client without a default TLS crypto provider, so the
//! driver installs one before using it.
//!
//! Usage:
//!
//! ```text
//! driver lookup-endpoints --scheduler <endpoint> <url>
//! driver get --scheduler <endpoint> [--endpoint <endpoint>]... [--header <key: value>]... --output <path> <url>
//! driver preheat --scheduler <endpoint> <url>
//! driver preheat-image --scheduler <endpoint> <image>
//! ```

use std::collections::BTreeMap;
use std::path::PathBuf;

use clap::{Parser, Subcommand};
use dragonfly_client_request::{
    GetRequest, PreheatImageRequest, PreheatRequest, Proxy, ProxyWithEndpoints, Request,
    RequestWithEndpoints,
};
use futures::TryStreamExt;
use http::header::{HeaderMap, HeaderName, HeaderValue};
use serde::Serialize;
use tokio::io::AsyncWriteExt;

/// The result of the driver commands.
type Result<T> = std::result::Result<T, Box<dyn std::error::Error>>;

/// Drives the Rust client-request SDK for the e2e tests.
#[derive(Parser)]
#[command(name = "driver")]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

/// The driver commands.
#[derive(Subcommand)]
enum Command {
    /// Looks up the proxy endpoints of the seed peers serving the url and
    /// prints them.
    LookupEndpoints {
        /// Scheduler endpoint.
        #[arg(long)]
        scheduler: String,

        url: String,
    },

    /// Downloads the url into the output file, via the seed peers the scheduler
    /// picks or via the given endpoints, and prints the response status and
    /// headers.
    Get {
        /// Scheduler endpoint.
        #[arg(long, required_unless_present = "endpoint")]
        scheduler: Option<String>,

        /// Seed peer endpoint to send the request to, repeatable.
        #[arg(long)]
        endpoint: Vec<String>,

        /// Request header "Key: Value", repeatable.
        #[arg(long)]
        header: Vec<String>,

        /// Output file path.
        #[arg(long)]
        output: PathBuf,

        url: String,
    },

    /// Preheats the url to the seed peers.
    Preheat {
        /// Scheduler endpoint.
        #[arg(long)]
        scheduler: String,

        url: String,
    },

    /// Preheats the image to the seed peers.
    PreheatImage {
        /// Scheduler endpoint.
        #[arg(long)]
        scheduler: String,

        image: String,
    },
}

/// The JSON output of the lookup-endpoints command.
#[derive(Serialize)]
struct LookupEndpointsResponse {
    /// The proxy endpoints of the seed peers serving the url.
    endpoints: Vec<String>,
}

/// The JSON output of the get command.
#[derive(Serialize)]
struct GetResponse {
    /// The status code of the response.
    status_code: u16,

    /// The headers of the response, with lower case keys.
    header: BTreeMap<String, String>,
}

#[tokio::main]
async fn main() -> Result<()> {
    let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();

    match Cli::parse().command {
        Command::LookupEndpoints { scheduler, url } => lookup_endpoints(scheduler, url).await,
        Command::Get {
            scheduler,
            endpoint,
            header,
            output,
            url,
        } => get(scheduler, endpoint, header, output, url).await,
        Command::Preheat { scheduler, url } => preheat(scheduler, url).await,
        Command::PreheatImage { scheduler, image } => preheat_image(scheduler, image).await,
    }
}

/// Looks up the proxy endpoints of the seed peers serving the url and prints
/// them.
async fn lookup_endpoints(scheduler_endpoint: String, url: String) -> Result<()> {
    let proxy = new_proxy(scheduler_endpoint).await?;
    let endpoints = proxy
        .lookup_endpoints(&GetRequest {
            url,
            ..Default::default()
        })
        .await?;
    println!(
        "{}",
        serde_json::to_string(&LookupEndpointsResponse { endpoints })?
    );

    Ok(())
}

/// Downloads the url into the output file, via the seed peers the scheduler
/// picks or via the given endpoints, and prints the response status and
/// headers.
async fn get(
    scheduler_endpoint: Option<String>,
    endpoints: Vec<String>,
    header: Vec<String>,
    output: PathBuf,
    url: String,
) -> Result<()> {
    let request = GetRequest {
        url,
        header: parse_header(&header)?,
        ..Default::default()
    };

    let response = if endpoints.is_empty() {
        let proxy = new_proxy(scheduler_endpoint.ok_or("--scheduler is required")?).await?;
        proxy.get(&request).await?
    } else {
        let proxy_with_endpoints = ProxyWithEndpoints::builder()
            .endpoints(endpoints)
            .build()
            .await?;
        proxy_with_endpoints.get(&request).await?
    };

    let mut body = response.body.ok_or("missing response body")?;
    let mut file = tokio::fs::File::create(&output).await?;
    while let Some(chunk) = body.try_next().await? {
        file.write_all(&chunk).await?;
    }
    file.flush().await?;

    println!(
        "{}",
        serde_json::to_string(&GetResponse {
            status_code: response
                .status_code
                .ok_or("missing response status code")?
                .as_u16(),
            header: lower_header(&response.header),
        })?
    );

    Ok(())
}

/// Preheats the url to the seed peers.
async fn preheat(scheduler_endpoint: String, url: String) -> Result<()> {
    let proxy = new_proxy(scheduler_endpoint).await?;
    proxy
        .preheat(&PreheatRequest {
            url,
            ..Default::default()
        })
        .await?;

    Ok(())
}

/// Preheats the image to the seed peers.
async fn preheat_image(scheduler_endpoint: String, image: String) -> Result<()> {
    let proxy = new_proxy(scheduler_endpoint).await?;
    proxy
        .preheat_image(&PreheatImageRequest {
            image,
            ..Default::default()
        })
        .await?;

    Ok(())
}

/// Creates a Proxy connected to the scheduler.
async fn new_proxy(scheduler_endpoint: String) -> Result<Proxy> {
    Ok(Proxy::builder()
        .scheduler_endpoint(scheduler_endpoint)
        .build()
        .await?)
}

/// Parses the repeatable "Key: Value" header flags.
fn parse_header(values: &[String]) -> Result<HeaderMap> {
    let mut header = HeaderMap::with_capacity(values.len());
    for value in values {
        let (key, val) = value
            .split_once(':')
            .ok_or_else(|| format!("invalid header {value:?}, expected \"Key: Value\""))?;
        header.append(
            HeaderName::from_bytes(key.trim().as_bytes())?,
            HeaderValue::from_str(val.trim())?,
        );
    }

    Ok(header)
}

/// Flattens the header to a map with lower case keys, the last value wins for
/// duplicate keys, matching the Go driver.
fn lower_header(header: &HeaderMap) -> BTreeMap<String, String> {
    header
        .iter()
        .filter_map(|(key, value)| {
            value
                .to_str()
                .ok()
                .map(|value| (key.as_str().to_string(), value.to_string()))
        })
        .collect()
}
