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
//! driver preheat --scheduler <endpoint> [--scope <scope>] <url>
//! driver preheat-image --scheduler <endpoint> [--scope <scope>] [--platform <platform>] <image>
//! driver delete --scheduler <endpoint> [--scope <scope>] <url>
//! driver delete-image --scheduler <endpoint> [--platform <platform>] <image>
//! ```

use std::collections::BTreeMap;
use std::path::PathBuf;

use clap::{Parser, Subcommand, ValueEnum};
use dragonfly_client_request::{
    DeleteImageRequest, DeleteRequest, GetRequest, PreheatImageRequest, PreheatRequest, Proxy,
    ProxyWithEndpoints, Request, RequestWithEndpoints, Scope,
};
use futures::TryStreamExt;
use http::header::{HeaderMap, HeaderName, HeaderValue};
use serde_json::json;
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

        /// Seed peers to address.
        #[arg(long, value_enum, default_value_t = ScopeArg::Default)]
        scope: ScopeArg,

        /// Url to preheat.
        url: String,
    },

    /// Preheats the image to the seed peers, all its platforms unless the platform
    /// is given.
    PreheatImage {
        /// Scheduler endpoint.
        #[arg(long)]
        scheduler: String,

        /// Seed peers to address.
        #[arg(long, value_enum, default_value_t = ScopeArg::Default)]
        scope: ScopeArg,

        /// Platform of the image in the format "os/arch", all the platforms by
        /// default.
        #[arg(long)]
        platform: Option<String>,

        /// Image reference, e.g., "docker.io/library/busybox:latest".
        image: String,
    },

    /// Deletes the preheated url from the seed peers.
    Delete {
        /// Scheduler endpoint.
        #[arg(long)]
        scheduler: String,

        /// Seed peers to address.
        #[arg(long, value_enum, default_value_t = ScopeArg::Default)]
        scope: ScopeArg,

        /// Url to preheat.
        url: String,
    },

    /// Deletes the preheated image from all seed peers, all its platforms unless
    /// the platform is given.
    DeleteImage {
        /// Scheduler endpoint.
        #[arg(long)]
        scheduler: String,

        /// Platform of the image in the format "os/arch", all the platforms by
        /// default.
        #[arg(long)]
        platform: Option<String>,

        /// Image reference, e.g., "docker.io/library/busybox:latest".
        image: String,
    },
}

/// The values of the scope flag, the same as the Go driver.
#[derive(Clone, Copy, ValueEnum)]
#[value(rename_all = "snake_case")]
enum ScopeArg {
    /// The replicas of the task.
    Default,

    /// All seed peers.
    AllSeedPeers,
}

/// Converts the scope flag to the scope of the SDK.
impl From<ScopeArg> for Scope {
    fn from(scope: ScopeArg) -> Self {
        match scope {
            ScopeArg::Default => Scope::Default,
            ScopeArg::AllSeedPeers => Scope::AllSeedPeers,
        }
    }
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
        Command::Preheat {
            scheduler,
            scope,
            url,
        } => preheat(scheduler, scope.into(), url).await,
        Command::PreheatImage {
            scheduler,
            scope,
            platform,
            image,
        } => preheat_image(scheduler, scope.into(), platform, image).await,
        Command::Delete {
            scheduler,
            scope,
            url,
        } => delete(scheduler, scope.into(), url).await,
        Command::DeleteImage {
            scheduler,
            platform,
            image,
        } => delete_image(scheduler, platform, image).await,
    }
}

/// Looks up the proxy endpoints of the seed peers serving the url and prints
/// them.
async fn lookup_endpoints(scheduler_endpoint: String, url: String) -> Result<()> {
    let proxy = Proxy::builder()
        .scheduler_endpoint(scheduler_endpoint)
        .build()
        .await?;
    let endpoints = proxy
        .lookup_endpoints(&GetRequest {
            url,
            ..Default::default()
        })
        .await?;
    println!("{}", json!({ "endpoints": endpoints }));

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
    let mut request_header = HeaderMap::with_capacity(header.len());
    for value in &header {
        let (key, val) = value
            .split_once(':')
            .ok_or_else(|| format!("invalid header {value:?}, expected \"Key: Value\""))?;
        request_header.append(
            HeaderName::from_bytes(key.trim().as_bytes())?,
            HeaderValue::from_str(val.trim())?,
        );
    }

    let request = GetRequest {
        url,
        header: request_header,
        ..Default::default()
    };

    let response = if endpoints.is_empty() {
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint.ok_or("--scheduler is required")?)
            .build()
            .await?;
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

    // Flatten the header to a map with lower case keys, the last value wins for
    // duplicate keys, matching the Go driver.
    let status_code = response.status_code.ok_or("missing response status code")?;
    let header: BTreeMap<String, String> = response
        .header
        .iter()
        .filter_map(|(key, value)| {
            value
                .to_str()
                .ok()
                .map(|value| (key.as_str().to_string(), value.to_string()))
        })
        .collect();
    println!(
        "{}",
        json!({ "status_code": status_code.as_u16(), "header": header })
    );

    Ok(())
}

/// Preheats the url to the seed peers.
async fn preheat(scheduler_endpoint: String, scope: Scope, url: String) -> Result<()> {
    let proxy = Proxy::builder()
        .scheduler_endpoint(scheduler_endpoint)
        .build()
        .await?;
    proxy
        .preheat(&PreheatRequest {
            url,
            scope,
            ..Default::default()
        })
        .await?;

    Ok(())
}

/// Preheats the image to the seed peers, all its platforms unless the platform
/// is given.
async fn preheat_image(
    scheduler_endpoint: String,
    scope: Scope,
    platform: Option<String>,
    image: String,
) -> Result<()> {
    let proxy = Proxy::builder()
        .scheduler_endpoint(scheduler_endpoint)
        .build()
        .await?;
    proxy
        .preheat_image(&PreheatImageRequest {
            image,
            platform,
            scope,
            ..Default::default()
        })
        .await?;

    Ok(())
}

/// Deletes the preheated url from the seed peers.
async fn delete(scheduler_endpoint: String, scope: Scope, url: String) -> Result<()> {
    let proxy = Proxy::builder()
        .scheduler_endpoint(scheduler_endpoint)
        .build()
        .await?;
    proxy
        .delete(&DeleteRequest {
            url,
            scope,
            ..Default::default()
        })
        .await?;

    Ok(())
}

/// Deletes the preheated image from all seed peers, all its platforms unless the
/// platform is given, and prints the tasks every seed peer deleted and retained.
async fn delete_image(
    scheduler_endpoint: String,
    platform: Option<String>,
    image: String,
) -> Result<()> {
    let proxy = Proxy::builder()
        .scheduler_endpoint(scheduler_endpoint)
        .build()
        .await?;
    let response = proxy
        .delete_image(&DeleteImageRequest {
            image,
            platform,
            ..Default::default()
        })
        .await?;
    let peers: Vec<_> = response
        .peers
        .iter()
        .map(|peer| {
            json!({
                "ip": peer.ip,
                "hostname": peer.hostname,
                "deleted_task_ids": peer.deleted_task_ids,
                "retained_task_ids": peer.retained_task_ids,
            })
        })
        .collect();
    println!("{}", json!({ "peers": peers }));

    Ok(())
}
