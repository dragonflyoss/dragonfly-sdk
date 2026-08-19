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

//! Request library for the Dragonfly client. It sends requests to remote
//! servers via the Dragonfly P2P network, supporting streaming and buffered
//! GET requests and preheating files or OCI images through seed peers.

use async_trait::async_trait;
use bytes::{Bytes, BytesMut};
use digest::is_blob_url;
use dragonfly_api::common::v2::{Download, Priority, TaskType};
use dragonfly_api::dfdaemon::v2::{
    dfdaemon_upload_client::DfdaemonUploadClient as DfdaemonUploadGRPCClient, DownloadTaskRequest,
};
use dragonfly_api::scheduler::v2::scheduler_client::SchedulerClient;
use errors::{BackendError, DfdaemonError, Error, ProxyError};
use futures::{Stream, TryStreamExt};
use http::{default_proxy_rule_filtered_query_params, headermap_to_hashmap};
use id_generator::{IDGenerator, TaskIDParameter};
use net::{format_url, preferred_local_ip};
use pool::{Builder as PoolBuilder, Factory, Pool};
use rand::seq::SliceRandom;
use reqwest::{
    header::{HeaderMap, HeaderValue},
    Client,
};
use reqwest_middleware::{ClientBuilder, ClientWithMiddleware};
use reqwest_tracing::TracingMiddleware;
use rustls_pki_types::CertificateDer;
use selector::{SeedPeerSelector, Selector};
use std::collections::HashMap;
use std::net::IpAddr;
use std::str::FromStr;
use std::sync::Arc;
use std::time::Duration;
use tonic::transport::{Channel, Endpoint};
use tracing::{debug, warn};

#[cfg(feature = "preheat")]
use oci_client::{
    client::ClientConfig, manifest::ImageIndexEntry, secrets::RegistryAuth, Client as OciClient,
    Reference, RegistryOperation,
};
#[cfg(feature = "preheat")]
use oci_spec::image::{Arch, Os};
#[cfg(feature = "preheat")]
use reqwest::header::AUTHORIZATION;
#[cfg(feature = "preheat")]
use tokio::sync::Semaphore;
use tokio::task::JoinSet;
use tracing::Instrument;

pub mod errors;
pub mod hashring;
pub mod id_generator;

mod digest;
mod http;
mod net;
mod pool;
mod selector;
mod shutdown;

/// The max idle connections per host.
const POOL_MAX_IDLE_PER_HOST: usize = 1024;

/// The maximum amount of time an idle connection remains idle in the pool
/// before closing itself.
const POOL_IDLE_TIMEOUT: Duration = Duration::from_secs(90);

/// The maximum amount of time a connect waits to complete.
const CONNECT_TIMEOUT: Duration = Duration::from_secs(30);

/// The keep alive interval for TCP connection.
const KEEP_ALIVE_INTERVAL: Duration = Duration::from_secs(60);

/// The default idle timeout(30 minutes) for clients in the pool.
const DEFAULT_CLIENT_POOL_IDLE_TIMEOUT: Duration = Duration::from_secs(30 * 60);

/// The default capacity of the client pool.
const DEFAULT_CLIENT_POOL_CAPACITY: usize = 128;

/// The default timeout(5 seconds) for requests to the
/// scheduler service.
const DEFAULT_SCHEDULER_REQUEST_TIMEOUT: Duration = Duration::from_secs(5);

/// The default timeout(30 minutes) for requests.
const DEFAULT_REQUEST_TIMEOUT: Duration = Duration::from_secs(30 * 60);

/// The default number of seed peers serving a task.
const DEFAULT_REPLICAS: usize = 2;

/// A specialized Result type for the proxy module.
pub type Result<T> = std::result::Result<T, Error>;

/// The type alias for the response body stream of zero-copy `Bytes` chunks.
pub type Body = Box<dyn Stream<Item = Result<Bytes>> + Send + Unpin>;

/// Defines the interface for sending requests via the Dragonfly.
///
/// This trait enables interaction with remote servers through the Dragonfly, providing methods
/// for performing GET requests with flexible response handling. It is designed for clients that
/// need to communicate with Dragonfly seed client efficiently, supporting both streaming and buffered
/// response processing. The trait shields the complex request logic between the client and the
/// Dragonfly seed client's proxy, abstracting the underlying communication details to simplify
/// client implementation and usage.
#[async_trait]
pub trait Request {
    /// Sends an GET request to a remote server via the Dragonfly and returns a response
    /// with a streaming body.
    ///
    /// This method is designed for scenarios where the response body is expected to be processed as a
    /// stream, allowing efficient handling of large or continuous data. The response includes metadata
    /// such as status codes and headers, along with a streaming `Body` for accessing the response content.
    async fn get(&self, request: &GetRequest) -> Result<GetResponse<Body>>;

    /// Sends an GET request to a remote server via the Dragonfly and writes the response
    /// body directly into the provided buffer.
    ///
    /// This method is optimized for scenarios where the response body needs to be stored directly in
    /// memory, avoiding the overhead of streaming for smaller or fixed-size responses. The provided
    /// `BytesMut` buffer is used to store the response content, and the response metadata (e.g., status
    /// and headers) is returned separately.
    async fn get_into(&self, request: &GetRequest, buf: &mut BytesMut) -> Result<GetResponse>;

    /// Preheats an OCI image by downloading all its blobs via the Dragonfly.
    ///
    /// This method is designed for scenarios where OCI image content needs to be pre-cached in
    /// the seed client before actual consumption, ensuring faster subsequent access across the
    /// cluster. It parses the image reference, authenticates with the OCI registry, resolves
    /// the image manifest (including multi-platform image indexes), and triggers the seed
    /// client to download each blob (config and layers), without streaming the blob content
    /// back to the client.
    #[cfg(feature = "preheat")]
    async fn preheat_image(&self, request: &PreheatImageRequest) -> Result<()>;

    /// Preheats a file by downloading it to the replicas of seed peers via the Dragonfly.
    ///
    /// This method is designed for scenarios where file content needs to be pre-cached in
    /// the seed client before actual consumption, ensuring faster subsequent access across
    /// the cluster. It triggers every replica seed peer to download the file by the
    /// dfdaemon download task API.
    async fn preheat(&self, request: &PreheatRequest) -> Result<()>;

    /// Looks up the endpoints of the seed peers serving the request, in the consistent
    /// hash ring selection order for the request's task id.
    ///
    /// This method is designed for scenarios where clients need to know which seed peers
    /// would serve the request without sending it. It returns up to the replicas of the
    /// request distinct endpoints, clamped to the number of available seed peers.
    async fn lookup_endpoints(&self, request: &GetRequest) -> Result<Vec<String>>;
}

/// Defines the interface for sending requests via fixed seed peer endpoints of the Dragonfly.
///
/// Unlike `Request`, implementations send requests to the seed peer endpoints given at
/// construction (e.g., the ones returned by `Request::lookup_endpoints`), without selecting
/// seed peers by the consistent hash ring or syncing them from the scheduler.
#[async_trait]
pub trait RequestWithEndpoints {
    /// Sends an GET request to a remote server via the seed peer endpoints of the
    /// Dragonfly and returns a response with a streaming body. The request is sent to a
    /// randomly picked endpoint and retried on the others up to the max retries.
    async fn get(&self, request: &GetRequest) -> Result<GetResponse<Body>>;

    /// Sends an GET request to a remote server via the seed peer endpoints of the
    /// Dragonfly and writes the response body directly into the provided buffer. The
    /// request is sent to a randomly picked endpoint and retried on the others up to the
    /// max retries.
    async fn get_into(&self, request: &GetRequest, buf: &mut BytesMut) -> Result<GetResponse>;
}

/// Represents a GET request to be sent via the Dragonfly.
pub struct GetRequest {
    /// The url of the request.
    pub url: String,

    /// The headers of the request.
    pub header: HeaderMap,

    /// Task piece length.
    pub piece_length: Option<u64>,

    /// URL tag identifies different task for same url.
    pub tag: Option<String>,

    /// Application of task identifies different task for same url.
    pub application: Option<String>,

    /// Filtered query params to generate the task id.
    /// When filter is ["Signature", "Expires", "ns"], for example:
    /// http://example.com/xyz?Expires=e1&Signature=s1&ns=docker.io and http://example.com/xyz?Expires=e2&Signature=s2&ns=docker.io
    /// will generate the same task id.
    /// Default value includes the filtered query params of s3, gcs, oss, obs, cos.
    pub filtered_query_params: Vec<String>,

    /// Content for calculating task id. This is used when the task ID cannot be calculated based
    /// on URL and other parameters, such as when the URL contains dynamic query parameters that
    /// cannot be filtered out.
    pub content_for_calculating_task_id: Option<String>,

    /// Enable task id based blob digest. It indicates whether to use the blob digest for task ID calculation
    /// when downloading from OCI registries. When enabled for OCI blob URLs (e.g., /v2/<name>/blobs/sha256:<digest>),
    /// the task ID is derived from the blob digest rather than the full URL. This enables deduplication across
    /// registries - the same blob from different registries shares one task ID, eliminating redundant downloads
    /// and storage, default is true.
    pub enable_task_id_based_blob_digest: bool,

    /// Refer to https://github.com/dragonflyoss/api/blob/main/proto/common.proto#L67
    pub priority: Option<i32>,

    /// The number of seed peers serving the task, default is 2.
    pub replicas: usize,

    /// The timeout of the request, default is 30 minutes.
    pub timeout: Duration,

    /// The client certificates for the request.
    pub client_cert: Option<Vec<CertificateDer<'static>>>,
}

/// Default implementation for GetRequest.
impl Default for GetRequest {
    /// Returns a default GetRequest with empty url and default values for other fields.
    fn default() -> Self {
        Self {
            url: String::new(),
            header: HeaderMap::new(),
            piece_length: None,
            tag: None,
            application: None,
            filtered_query_params: default_proxy_rule_filtered_query_params(),
            content_for_calculating_task_id: None,
            enable_task_id_based_blob_digest: true,
            priority: None,
            replicas: DEFAULT_REPLICAS,
            timeout: DEFAULT_REQUEST_TIMEOUT,
            client_cert: None,
        }
    }
}

/// Implements methods for validating the request.
impl GetRequest {
    /// Validates the request parameters.
    fn validate(&self) -> Result<()> {
        if self.replicas == 0 {
            return Err(Error::InvalidArgument(
                "replicas must be positive".to_string(),
            ));
        }

        Ok(())
    }
}

/// Represents a GET response received via the Dragonfly.
pub struct GetResponse<R = Body> {
    /// The success of the response.
    pub success: bool,

    /// The headers of the response.
    pub header: HeaderMap,

    /// The status code of the response.
    pub status_code: Option<reqwest::StatusCode>,

    /// The body of the response.
    pub body: Option<R>,
}

/// Represents a request to preheat an OCI image through the
/// Dragonfly seed client. The preheat downloads all blobs (config and layers)
/// of the specified image via the Dragonfly proxy, effectively caching them
/// in the P2P network for faster downloading.
#[cfg(feature = "preheat")]
pub struct PreheatImageRequest {
    /// The OCI image reference (e.g., "docker.io/library/nginx:latest").
    pub image: String,

    /// Username for registry authentication. If not provided, anonymous access is used.
    pub username: Option<String>,

    /// Password for registry authentication. If not provided, anonymous access is used.
    pub password: Option<String>,

    /// Platform specifies the target platform in the format "os/arch"
    /// (e.g., "linux/amd64", "linux/arm64"). This is used to select the correct
    /// manifest from a multi-platform image index, default is current platform.
    pub platform: Option<String>,

    /// The optional piece length for the Dragonfly task.
    pub piece_length: Option<u64>,

    /// Tag identifies different tasks for the same URL.
    pub tag: Option<String>,

    /// Application identifies different tasks for the same URL.
    pub application: Option<String>,

    /// Filtered query params to generate the task id.
    /// When filter is ["Signature", "Expires", "ns"], for example:
    /// http://example.com/xyz?Expires=e1&Signature=s1&ns=docker.io and http://example.com/xyz?Expires=e2&Signature=s2&ns=docker.io
    /// will generate the same task id.
    /// Default value includes the filtered query params of s3, gcs, oss, obs, cos.
    pub filtered_query_params: Vec<String>,

    /// Content for calculating task id. This is used when the task ID cannot be calculated based
    /// on URL and other parameters, such as when the URL contains dynamic query parameters that
    /// cannot be filtered out.
    pub content_for_calculating_task_id: Option<String>,

    /// Enable task id based blob digest. It indicates whether to use the blob digest for task ID calculation
    /// when downloading from OCI registries. When enabled for OCI blob URLs (e.g., /v2/<name>/blobs/sha256:<digest>),
    /// the task ID is derived from the blob digest rather than the full URL. This enables deduplication across
    /// registries - the same blob from different registries shares one task ID, eliminating redundant downloads
    /// and storage, default is true.
    pub enable_task_id_based_blob_digest: bool,

    /// Refer to https://github.com/dragonflyoss/api/blob/main/proto/common.proto#L67
    pub priority: Option<i32>,

    /// The number of seed peers serving the task, default is 2.
    pub replicas: usize,

    /// The timeout for each blob download request, default is 30 minutes.
    pub timeout: Duration,

    /// The number of blobs to preheat concurrently, default is 4.
    pub concurrent_task_count: usize,

    /// The optional client certificates for the request.
    pub client_cert: Option<Vec<CertificateDer<'static>>>,
}

/// Default implementation for PreheatImageRequest.
#[cfg(feature = "preheat")]
impl Default for PreheatImageRequest {
    /// Returns a default PreheatImageRequest with empty image and default values for other
    /// fields.
    fn default() -> Self {
        Self {
            image: String::new(),
            username: None,
            password: None,
            platform: None,
            piece_length: None,
            tag: None,
            application: None,
            filtered_query_params: default_proxy_rule_filtered_query_params(),
            content_for_calculating_task_id: None,
            enable_task_id_based_blob_digest: true,
            priority: None,
            replicas: DEFAULT_REPLICAS,
            timeout: DEFAULT_REQUEST_TIMEOUT,
            concurrent_task_count: 4,
            client_cert: None,
        }
    }
}

#[cfg(feature = "preheat")]
/// Implements methods for validating the request.
impl PreheatImageRequest {
    /// Validates the request parameters.
    fn validate(&self) -> Result<()> {
        if self.replicas == 0 {
            return Err(Error::InvalidArgument(
                "replicas must be positive".to_string(),
            ));
        }

        if self.concurrent_task_count == 0 {
            return Err(Error::InvalidArgument(
                "concurrent task count must be positive".to_string(),
            ));
        }

        Ok(())
    }
}

/// Represents a request to preheat a file through the Dragonfly seed client. The preheat
/// downloads the specified file via the Dragonfly proxy, effectively caching it in the P2P
/// network for faster downloading.
pub struct PreheatRequest {
    /// The url of the request.
    pub url: String,

    /// The headers of the request.
    pub header: HeaderMap,

    /// Task piece length.
    pub piece_length: Option<u64>,

    /// URL tag identifies different task for same url.
    pub tag: Option<String>,

    /// Application of task identifies different task for same url.
    pub application: Option<String>,

    /// Filtered query params to generate the task id.
    /// When filter is ["Signature", "Expires", "ns"], for example:
    /// http://example.com/xyz?Expires=e1&Signature=s1&ns=docker.io and http://example.com/xyz?Expires=e2&Signature=s2&ns=docker.io
    /// will generate the same task id.
    /// Default value includes the filtered query params of s3, gcs, oss, obs, cos.
    pub filtered_query_params: Vec<String>,

    /// Content for calculating task id. This is used when the task ID cannot be calculated based
    /// on URL and other parameters, such as when the URL contains dynamic query parameters that
    /// cannot be filtered out.
    pub content_for_calculating_task_id: Option<String>,

    /// Enable task id based blob digest. It indicates whether to use the blob digest for task ID calculation
    /// when downloading from OCI registries. When enabled for OCI blob URLs (e.g., /v2/<name>/blobs/sha256:<digest>),
    /// the task ID is derived from the blob digest rather than the full URL. This enables deduplication across
    /// registries - the same blob from different registries shares one task ID, eliminating redundant downloads
    /// and storage, default is true.
    pub enable_task_id_based_blob_digest: bool,

    /// Refer to https://github.com/dragonflyoss/api/blob/main/proto/common.proto#L67
    pub priority: Option<i32>,

    /// The number of seed peers serving the task, default is 2.
    pub replicas: usize,

    /// The timeout of the request, default is 30 minutes.
    pub timeout: Duration,

    /// The client certificates for the request.
    pub client_cert: Option<Vec<CertificateDer<'static>>>,
}

/// Default implementation for PreheatRequest.
impl Default for PreheatRequest {
    /// Returns a default PreheatRequest with empty image and default values for other fields.
    fn default() -> Self {
        Self {
            url: String::new(),
            header: HeaderMap::new(),
            piece_length: None,
            tag: None,
            application: None,
            filtered_query_params: default_proxy_rule_filtered_query_params(),
            content_for_calculating_task_id: None,
            enable_task_id_based_blob_digest: true,
            priority: None,
            replicas: DEFAULT_REPLICAS,
            timeout: DEFAULT_REQUEST_TIMEOUT,
            client_cert: None,
        }
    }
}

/// Implements methods for validating the request.
impl PreheatRequest {
    /// Validates the request parameters.
    fn validate(&self) -> Result<()> {
        if self.replicas == 0 {
            return Err(Error::InvalidArgument(
                "replicas must be positive".to_string(),
            ));
        }

        Ok(())
    }
}

/// Factory for creating HTTPClient instances.
#[derive(Debug, Clone, Default)]
struct HTTPClientFactory {}

/// Implements Factory for creating reqwest::Client instances with proxy support.
#[async_trait]
impl Factory<String, ClientWithMiddleware> for HTTPClientFactory {
    type Error = Error;

    /// Creates a new reqwest::Client configured to use the specified proxy address.
    async fn make_client(&self, proxy_addr: &String) -> Result<ClientWithMiddleware> {
        // TODO(chlins): Support client certificates and set `danger_accept_invalid_certs`
        // based on the certificates.
        let client = Client::builder()
            .hickory_dns(true)
            .danger_accept_invalid_certs(true)
            .pool_max_idle_per_host(POOL_MAX_IDLE_PER_HOST)
            .pool_idle_timeout(POOL_IDLE_TIMEOUT)
            .tcp_keepalive(KEEP_ALIVE_INTERVAL)
            .connect_timeout(CONNECT_TIMEOUT)
            .proxy(reqwest::Proxy::all(proxy_addr).map_err(|err| {
                Error::Internal(format!("failed to set proxy {proxy_addr}: {err}"))
            })?)
            .build()
            .map_err(|err| Error::Internal(format!("failed to build reqwest client: {err}")))?;

        Ok(ClientBuilder::new(client)
            .with(TracingMiddleware::default())
            .build())
    }
}

/// The builder for Proxy.
pub struct ProxyBuilder {
    /// The endpoint of the scheduler service.
    scheduler_endpoint: String,

    /// The timeout of the request to the scheduler service.
    scheduler_request_timeout: Duration,

    /// The interval of health check for selector(seed peers).
    health_check_interval: Duration,

    /// The number of times to retry a request.
    max_retries: u8,
}

/// Implements Default trait.
impl Default for ProxyBuilder {
    /// Returns a default ProxyBuilder.
    fn default() -> Self {
        Self {
            scheduler_endpoint: "".to_string(),
            scheduler_request_timeout: DEFAULT_SCHEDULER_REQUEST_TIMEOUT,
            health_check_interval: Duration::from_secs(60),
            max_retries: 1,
        }
    }
}

/// Implements the builder pattern for Proxy.
impl ProxyBuilder {
    /// Sets the scheduler endpoint.
    pub fn scheduler_endpoint(mut self, endpoint: String) -> Self {
        self.scheduler_endpoint = endpoint;
        self
    }

    /// Sets the scheduler request timeout.
    pub fn scheduler_request_timeout(mut self, timeout: Duration) -> Self {
        self.scheduler_request_timeout = timeout;
        self
    }

    /// Sets the health check interval.
    pub fn health_check_interval(mut self, interval: Duration) -> Self {
        self.health_check_interval = interval;
        self
    }

    /// Sets the maximum number of retries.
    pub fn max_retries(mut self, retries: u8) -> Self {
        self.max_retries = retries;
        self
    }

    /// Builds and returns a Proxy instance.
    pub async fn build(self) -> Result<Proxy> {
        // Validate input parameters.
        self.validate()?;

        // Create the scheduler channel.
        let scheduler_channel = Endpoint::from_shared(self.scheduler_endpoint.to_string())
            .map_err(|err| Error::InvalidArgument(err.to_string()))?
            .connect_timeout(self.scheduler_request_timeout)
            .timeout(self.scheduler_request_timeout)
            .connect()
            .await
            .map_err(|err| {
                Error::Internal(format!(
                    "failed to connect to scheduler {}: {}",
                    self.scheduler_endpoint, err
                ))
            })?;

        // Create scheduler client.
        let scheduler_client = SchedulerClient::new(scheduler_channel);

        // Create seed peer selector.
        let seed_peer_selector = Arc::new(
            SeedPeerSelector::new(scheduler_client, self.health_check_interval)
                .await
                .map_err(|err| {
                    Error::Internal(format!("failed to create seed peer selector: {err}"))
                })?,
        );

        let seed_peer_selector_clone = seed_peer_selector.clone();
        tokio::spawn(async move {
            // Run the selector service in the background to refresh the seed peers periodically.
            seed_peer_selector_clone.run().await;
        });

        // Get local IP address and hostname.
        // In IPv6-only environments, IPv4 detection may fail, so we use a best-effort IPv4->IPv6 fallback.
        let local_ip = preferred_local_ip()
            .ok_or_else(|| {
                Error::Internal("failed to detect a preferred local IP address".to_string())
            })?
            .to_string();
        let hostname = hostname::get()
            .map_err(|err| Error::Internal(format!("failed to get hostname: {err}")))?
            .to_string_lossy()
            .to_string();
        let id_generator = IDGenerator::new(local_ip, hostname, true);
        let proxy = Proxy {
            seed_peer_selector,
            max_retries: self.max_retries,
            client_pool: Arc::new(
                PoolBuilder::new(HTTPClientFactory::default())
                    .capacity(DEFAULT_CLIENT_POOL_CAPACITY)
                    .idle_timeout(DEFAULT_CLIENT_POOL_IDLE_TIMEOUT)
                    .build(),
            ),
            id_generator: Arc::new(id_generator),
        };

        Ok(proxy)
    }

    /// Validates the input parameters.
    fn validate(&self) -> Result<()> {
        if let Err(err) = url::Url::parse(&self.scheduler_endpoint) {
            return Err(Error::InvalidArgument(err.to_string()));
        };

        if self.scheduler_request_timeout.as_millis() < 100 {
            return Err(Error::InvalidArgument(
                "scheduler request timeout must be at least 100 milliseconds".to_string(),
            ));
        }

        if self.health_check_interval.as_secs() < 1 || self.health_check_interval.as_secs() > 600 {
            return Err(Error::InvalidArgument(
                "health check interval must be between 1 and 600 seconds".to_string(),
            ));
        }

        if self.max_retries > 10 {
            return Err(Error::InvalidArgument(
                "max retries must be between 0 and 10".to_string(),
            ));
        }

        Ok(())
    }
}

/// The HTTP proxy client that sends requests via Dragonfly.
#[derive(Clone)]
pub struct Proxy {
    /// The selector service for selecting seed peers.
    seed_peer_selector: Arc<SeedPeerSelector>,

    /// The number of times to retry a request.
    max_retries: u8,

    /// The pool of clients.
    client_pool: Arc<Pool<String, String, ClientWithMiddleware, HTTPClientFactory>>,

    /// The task id generator.
    id_generator: Arc<IDGenerator>,
}

/// Implements the proxy client that sends requests via Dragonfly.
impl Proxy {
    /// Returns a new ProxyBuilder for Proxy.
    pub fn builder() -> ProxyBuilder {
        ProxyBuilder::default()
    }
}

/// Implements the interface for sending requests via the Dragonfly.
///
/// This struct enables interaction with remote servers through the Dragonfly, providing methods
/// for performing GET requests with flexible response handling. It is designed for clients that
/// need to communicate with Dragonfly seed client efficiently, supporting both streaming and buffered
/// response processing. The trait shields the complex request logic between the client and the
/// Dragonfly seed client's proxy, abstracting the underlying communication details to simplify
/// client implementation and usage.
#[async_trait]
impl Request for Proxy {
    /// Sends an GET request to a remote server via the Dragonfly and returns a response
    /// with a streaming body.
    ///
    /// This method is designed for scenarios where the response body is expected to be processed as a
    /// stream, allowing efficient handling of large or continuous data. The response includes metadata
    /// such as status codes and headers, along with a streaming `Body` of `Bytes` chunks
    /// for accessing the response content.
    async fn get(&self, request: &GetRequest) -> Result<GetResponse> {
        request.validate()?;

        let get = async {
            let response = self.try_send(request).await?;
            let header = response.headers().clone();
            let status_code = response.status();
            let body: Body = Box::new(
                response
                    .bytes_stream()
                    .map_err(|err| Error::Internal(err.to_string())),
            );

            Ok(GetResponse {
                success: status_code.is_success(),
                header,
                status_code: Some(status_code),
                body: Some(body),
            })
        };

        tokio::time::timeout(request.timeout, get)
            .await
            .map_err(|err| Error::RequestTimeout(err.to_string()))?
    }

    /// Sends an GET request to a remote server via the Dragonfly and writes the response
    /// body directly into the provided buffer.
    ///
    /// This method is optimized for scenarios where the response body needs to be stored directly in
    /// memory, avoiding the overhead of streaming for smaller or fixed-size responses. The provided
    /// `BytesMut` buffer is used to store the response content, and the response metadata (e.g., status
    /// and headers) is returned separately.
    async fn get_into(&self, request: &GetRequest, buf: &mut BytesMut) -> Result<GetResponse> {
        request.validate()?;

        let get_into = async {
            let mut response = self.try_send(request).await?;
            let status = response.status();
            let header = response.headers().clone();

            if status.is_success() {
                // Reserve the capacity upfront and copy each chunk into the buffer
                // directly, without aggregating the whole body first.
                if let Some(content_length) = response.content_length() {
                    buf.reserve(content_length as usize);
                }

                while let Some(chunk) = response.chunk().await.map_err(|err| {
                    if err.is_timeout() {
                        return Error::RequestTimeout(err.to_string());
                    }

                    Error::Internal(format!("failed to read response body: {err}"))
                })? {
                    buf.extend_from_slice(&chunk);
                }
            }

            Ok(GetResponse {
                success: status.is_success(),
                header,
                status_code: Some(status),
                body: None,
            })
        };

        tokio::time::timeout(request.timeout, get_into)
            .await
            .map_err(|err| Error::RequestTimeout(err.to_string()))?
    }

    /// Preheats an OCI image by downloading all its blobs via the Dragonfly.
    ///
    /// This method is designed for scenarios where OCI image content needs to be pre-cached in
    /// the seed client before actual consumption, ensuring faster subsequent access across the
    /// cluster. It parses the image reference, authenticates with the OCI registry, resolves
    /// the image manifest (including multi-platform image indexes), and triggers the seed
    /// client to download each blob (config and layers).
    #[cfg(feature = "preheat")]
    async fn preheat_image(&self, request: &PreheatImageRequest) -> Result<()> {
        request.validate()?;

        // Parse image reference.
        let oci_client = Self::oci_client(request.platform.clone())?;
        let reference: Reference = request
            .image
            .parse()
            .map_err(|err| Error::InvalidArgument(format!("invalid image reference: {err}")))?;

        // Create registry authentication.
        let auth = match (&request.username, &request.password) {
            (Some(username), Some(password)) => {
                RegistryAuth::Basic(username.clone(), password.clone())
            }
            _ => RegistryAuth::Anonymous,
        };

        // Pull image manifest. This handles multi-platform image index manifests
        // by selecting the platform-specific manifest using our resolver.
        let (manifest, digest) = oci_client
            .pull_image_manifest(&reference, &auth)
            .await
            .map_err(|err| Error::Internal(format!("failed to pull image manifest: {err}")))?;
        debug!(
            "pulled manifest for image {} with digest {}, layers: {}",
            request.image,
            digest,
            manifest.layers.len()
        );

        // Authenticate with the registry and get a bearer token if available.
        let token = oci_client
            .auth(&reference, &auth, RegistryOperation::Pull)
            .await
            .map_err(|err| Error::Internal(format!("failed to authenticate with registry: {err}")))?
            .ok_or_else(|| {
                Error::Internal("registry did not return authentication token".to_string())
            })?;

        // Build authorization header for blob downloads through the Dragonfly.
        let mut header = HeaderMap::new();
        header.insert(
            AUTHORIZATION,
            HeaderValue::from_str(&format!("Bearer {token}"))
                .map_err(|err| Error::Internal(format!("invalid auth token: {err}")))?,
        );

        let registry = reference.resolve_registry();
        let repository = reference.repository();

        // Preheat the blobs concurrently, limited by the concurrent task count.
        let semaphore = Arc::new(Semaphore::new(request.concurrent_task_count));
        let mut join_set: JoinSet<Result<()>> = JoinSet::new();
        for digest in std::iter::once(&manifest.config.digest)
            .chain(manifest.layers.iter().map(|layer| &layer.digest))
        {
            let semaphore = semaphore.clone();
            let proxy = self.clone();
            let preheat_request = PreheatRequest {
                url: Self::build_blob_url(registry, repository, digest),
                header: header.clone(),
                piece_length: request.piece_length,
                tag: request.tag.clone(),
                application: request.application.clone(),
                filtered_query_params: request.filtered_query_params.clone(),
                content_for_calculating_task_id: request.content_for_calculating_task_id.clone(),
                enable_task_id_based_blob_digest: request.enable_task_id_based_blob_digest,
                priority: request.priority,
                replicas: request.replicas,
                timeout: request.timeout,
                client_cert: request.client_cert.clone(),
            };

            join_set.spawn(
                async move {
                    let _permit = semaphore
                        .acquire()
                        .await
                        .map_err(|err| Error::Internal(err.to_string()))?;

                    proxy.preheat(&preheat_request).await?;
                    debug!("preheated blob: {}", preheat_request.url);
                    Ok(())
                }
                .in_current_span(),
            );
        }

        // Wait for the preheats to finish.
        while let Some(result) = join_set.join_next().await {
            result.map_err(|err| Error::Internal(err.to_string()))??;
        }

        debug!("preheat completed for image: {}", request.image);
        Ok(())
    }

    /// Preheats a file by downloading it to the replicas of seed peers via the Dragonfly.
    ///
    /// This method is designed for scenarios where file content needs to be pre-cached in
    /// the seed client before actual consumption, ensuring faster subsequent access across
    /// the cluster. It triggers every replica seed peer to download the file by the
    /// dfdaemon download task API.
    async fn preheat(&self, request: &PreheatRequest) -> Result<()> {
        request.validate()?;

        // Generate task id for selecting seed peer.
        let task_id = self
            .id_generator
            .task_id(
                if let Some(content) = request.content_for_calculating_task_id.clone() {
                    TaskIDParameter::Content(content)
                } else if request.enable_task_id_based_blob_digest && is_blob_url(&request.url) {
                    TaskIDParameter::BlobDigestBased(request.url.clone())
                } else {
                    TaskIDParameter::URLBased {
                        url: request.url.clone(),
                        piece_length: request.piece_length,
                        tag: request.tag.clone(),
                        application: request.application.clone(),
                        filtered_query_params: request.filtered_query_params.clone(),
                        revision: None,
                    }
                },
            )
            .map_err(|err| Error::Internal(format!("failed to generate task id: {err}")))?;

        // Select seed peers for downloading.
        let seed_peers = self
            .seed_peer_selector
            .select(task_id.clone(), request.replicas as u32)
            .await
            .map_err(|err| {
                Error::Internal(format!("failed to select seed peers from scheduler: {err}"))
            })?;

        debug!("task {} selected seed peers: {:?}", task_id, seed_peers);

        if seed_peers.len() < request.replicas {
            return Err(Error::Internal(format!(
                "insufficient seed peers for {} replicas, {} available",
                request.replicas,
                seed_peers.len()
            )));
        }

        // Construct the download task request for preheating.
        let download_task_request = DownloadTaskRequest {
            download: Some(Download {
                url: request.url.clone(),
                r#type: TaskType::Standard as i32,
                tag: request.tag.clone(),
                application: request.application.clone(),
                priority: request.priority.unwrap_or(Priority::Level6 as i32),
                filtered_query_params: request.filtered_query_params.clone(),
                request_header: headermap_to_hashmap(&request.header),
                piece_length: request.piece_length,
                timeout: Some(
                    prost_wkt_types::Duration::try_from(request.timeout).map_err(|err| {
                        Error::InvalidArgument(format!("invalid request timeout: {err}"))
                    })?,
                ),
                content_for_calculating_task_id: request.content_for_calculating_task_id.clone(),
                remote_ip: preferred_local_ip().map(|ip| ip.to_string()),
                enable_task_id_based_blob_digest: request.enable_task_id_based_blob_digest,
                ..Default::default()
            }),
        };

        // Trigger every replica seed peer to download the task concurrently and
        // wait for the download tasks to finish, without streaming the file
        // content back to the client.
        let mut join_set: JoinSet<Result<()>> = JoinSet::new();
        for peer in seed_peers.iter() {
            let addr = format_url(
                "http",
                IpAddr::from_str(&peer.ip).map_err(|err| Error::Internal(err.to_string()))?,
                peer.port as u16,
            );

            let task_id = task_id.clone();
            let download_task_request = download_task_request.clone();
            let timeout = request.timeout;
            join_set.spawn(
                async move {
                    let channel = Channel::from_shared(addr.clone())
                        .map_err(|err| Error::InvalidArgument(err.to_string()))?
                        .connect_timeout(timeout)
                        .timeout(timeout)
                        .connect()
                        .await
                        .map_err(|err| {
                            Error::Internal(format!("failed to connect to seed peer {addr}: {err}"))
                        })?;

                    let mut client = DfdaemonUploadGRPCClient::new(channel)
                        .max_decoding_message_size(usize::MAX)
                        .max_encoding_message_size(usize::MAX);

                    let mut response = client
                        .download_task(download_task_request)
                        .await
                        .map_err(|err| {
                            Error::Internal(format!("failed to download task {task_id}: {err}"))
                        })?
                        .into_inner();

                    while response
                        .message()
                        .await
                        .map_err(|err| {
                            Error::Internal(format!("failed to download task {task_id}: {err}"))
                        })?
                        .is_some()
                    {}

                    Ok(())
                }
                .in_current_span(),
            );
        }

        // Wait for the download tasks on every replica seed peer to finish.
        while let Some(result) = join_set.join_next().await {
            result.map_err(|err| Error::Internal(err.to_string()))??;
        }

        Ok(())
    }

    /// Looks up the endpoints of the seed peers serving the request, in the consistent
    /// hash ring selection order for the request's task id. It returns up to the
    /// replicas of the request distinct endpoints, clamped to the number of available
    /// seed peers.
    async fn lookup_endpoints(&self, request: &GetRequest) -> Result<Vec<String>> {
        request.validate()?;

        // Generate task id for selecting seed peer.
        let task_id = self
            .id_generator
            .task_id(
                if let Some(content) = request.content_for_calculating_task_id.clone() {
                    TaskIDParameter::Content(content)
                } else if request.enable_task_id_based_blob_digest && is_blob_url(&request.url) {
                    TaskIDParameter::BlobDigestBased(request.url.clone())
                } else {
                    TaskIDParameter::URLBased {
                        url: request.url.clone(),
                        piece_length: request.piece_length,
                        tag: request.tag.clone(),
                        application: request.application.clone(),
                        filtered_query_params: request.filtered_query_params.clone(),
                        revision: None,
                    }
                },
            )
            .map_err(|err| Error::Internal(format!("failed to generate task id: {err}")))?;

        // Select seed peers for downloading.
        let seed_peers = self
            .seed_peer_selector
            .select(task_id.clone(), request.replicas as u32)
            .await
            .map_err(|err| {
                Error::Internal(format!("failed to select seed peers from scheduler: {err}"))
            })?;
        debug!("task {} selected seed peers: {:?}", task_id, seed_peers);

        let mut addrs = Vec::with_capacity(seed_peers.len());
        for peer in seed_peers.iter() {
            addrs.push(format_url(
                "http",
                IpAddr::from_str(&peer.ip).map_err(|err| Error::Internal(err.to_string()))?,
                peer.port as u16,
            ));
        }

        Ok(addrs)
    }
}

/// Implements proxy request logic.
impl Proxy {
    /// Looks up the proxy endpoints of the seed peers serving the request, in the
    /// consistent hash ring selection order.
    async fn lookup_proxy_endpoints(&self, request: &GetRequest) -> Result<Vec<String>> {
        // Generate task id for selecting seed peer.
        let task_id = self
            .id_generator
            .task_id(
                if let Some(content) = request.content_for_calculating_task_id.clone() {
                    TaskIDParameter::Content(content)
                } else if request.enable_task_id_based_blob_digest && is_blob_url(&request.url) {
                    TaskIDParameter::BlobDigestBased(request.url.clone())
                } else {
                    TaskIDParameter::URLBased {
                        url: request.url.clone(),
                        piece_length: request.piece_length,
                        tag: request.tag.clone(),
                        application: request.application.clone(),
                        filtered_query_params: request.filtered_query_params.clone(),
                        revision: None,
                    }
                },
            )
            .map_err(|err| Error::Internal(format!("failed to generate task id: {err}")))?;

        // Select seed peers for downloading.
        let seed_peers = self
            .seed_peer_selector
            .select(task_id.clone(), request.replicas as u32)
            .await
            .map_err(|err| {
                Error::Internal(format!("failed to select seed peers from scheduler: {err}"))
            })?;

        debug!("task {} selected seed peers: {:?}", task_id, seed_peers);

        let mut endpoints = Vec::with_capacity(seed_peers.len());
        for peer in seed_peers.iter() {
            // TODO(chlins): Support client https scheme.
            endpoints.push(format_url(
                "http",
                IpAddr::from_str(&peer.ip).map_err(|err| Error::Internal(err.to_string()))?,
                peer.proxy_port as u16,
            ));
        }

        Ok(endpoints)
    }

    /// Scatters the request across the given seed peer endpoints: it tries randomly
    /// picked endpoints one by one, limited by the max retries of the proxy.
    async fn send_with_endpoints(
        &self,
        endpoints: &[String],
        request: &GetRequest,
    ) -> Result<reqwest::Response> {
        if endpoints.is_empty() {
            return Err(Error::InvalidArgument(
                "no endpoints to send request".to_string(),
            ));
        }

        // Scatter the request across the endpoints: shuffle them and make
        // 1 + max retries attempts, wrapping around when the endpoints are
        // fewer than the attempts.
        let mut shuffled: Vec<&String> = endpoints.iter().collect();
        shuffled.shuffle(&mut rand::rng());

        let mut last_err = None;
        for attempt in 0..(self.max_retries as usize + 1) {
            let endpoint = shuffled[attempt % shuffled.len()];
            let entry = self.client_pool.entry(endpoint, endpoint).await?;
            match self.send(&entry.client, request).await {
                Ok(response) => return Ok(response),
                Err(err) => {
                    warn!("failed to send request to endpoint {}: {:?}", endpoint, err);
                    last_err = Some(err);
                }
            }
        }

        Err(last_err.unwrap_or_else(|| {
            Error::Internal("failed to send request to any endpoint".to_string())
        }))
    }

    /// Scatters the request across the seed peers serving it and returns the first
    /// successful response.
    async fn try_send(&self, request: &GetRequest) -> Result<reqwest::Response> {
        let endpoints = self.lookup_proxy_endpoints(request).await?;
        self.send_with_endpoints(&endpoints, request).await
    }

    /// Send a request to the specified URL via the client with the given headers.
    async fn send(
        &self,
        client: &ClientWithMiddleware,
        request: &GetRequest,
    ) -> Result<reqwest::Response> {
        let headers = self.make_request_headers(request)?;
        let response = client
            .get(&request.url)
            .headers(headers)
            .timeout(request.timeout)
            .send()
            .await
            .map_err(|err| match err {
                reqwest_middleware::Error::Reqwest(err) if err.is_timeout() => {
                    Error::RequestTimeout(err.to_string())
                }
                err => Error::Internal(err.to_string()),
            })?;

        let status = response.status();
        if status.is_success() {
            return Ok(response);
        }

        let response_headers = response.headers().clone();
        let header_map = headermap_to_hashmap(&response_headers);
        let message = response.text().await.ok();
        let error_type = response_headers
            .get("X-Dragonfly-Error-Type")
            .and_then(|v| v.to_str().ok());

        match error_type {
            Some("backend") => Err(Error::BackendError(BackendError {
                message,
                header: header_map,
                status_code: Some(status),
            })),
            Some("proxy") => Err(Error::ProxyError(ProxyError {
                message,
                header: header_map,
                status_code: Some(status),
            })),
            Some("dfdaemon") => Err(Error::DfdaemonError(DfdaemonError { message })),
            Some(other) => Err(Error::ProxyError(ProxyError {
                message: Some(format!("unknown error type from proxy: {other}")),
                header: header_map,
                status_code: Some(status),
            })),
            None => Err(Error::ProxyError(ProxyError {
                message: Some(format!("unexpected status code from proxy: {status}")),
                header: header_map,
                status_code: Some(status),
            })),
        }
    }

    /// Make request headers applies p2p related headers to the request headers.
    fn make_request_headers(&self, request: &GetRequest) -> Result<HeaderMap> {
        let mut headers = request.header.clone();
        if let Some(piece_length) = request.piece_length {
            headers.insert(
                "X-Dragonfly-Piece-Length",
                piece_length.to_string().parse().map_err(|err| {
                    Error::InvalidArgument(format!("invalid piece length: {err}"))
                })?,
            );
        }

        if let Some(tag) = request.tag.clone() {
            headers.insert(
                "X-Dragonfly-Tag",
                tag.to_string()
                    .parse()
                    .map_err(|err| Error::InvalidArgument(format!("invalid tag: {err}")))?,
            );
        }

        if let Some(application) = request.application.clone() {
            headers.insert(
                "X-Dragonfly-Application",
                application
                    .to_string()
                    .parse()
                    .map_err(|err| Error::InvalidArgument(format!("invalid application: {err}")))?,
            );
        }

        if let Some(content_for_calculating_task_id) =
            request.content_for_calculating_task_id.clone()
        {
            headers.insert(
                "X-Dragonfly-Content-For-Calculating-Task-ID",
                content_for_calculating_task_id
                    .to_string()
                    .parse()
                    .map_err(|err| {
                        Error::InvalidArgument(format!(
                            "invalid content for calculating task id: {err}"
                        ))
                    })?,
            );
        }

        headers.insert(
            "X-Dragonfly-Enable-Task-ID-Based-Blob-Digest",
            request
                .enable_task_id_based_blob_digest
                .to_string()
                .parse()
                .map_err(|err| {
                    Error::InvalidArgument(format!(
                        "invalid enable task id based blob digest: {err}"
                    ))
                })?,
        );

        if let Some(priority) = request.priority {
            headers.insert(
                "X-Dragonfly-Priority",
                priority
                    .to_string()
                    .parse()
                    .map_err(|err| Error::InvalidArgument(format!("invalid priority: {err}")))?,
            );
        }

        if !request.filtered_query_params.is_empty() {
            let value = request.filtered_query_params.join(",");
            headers.insert(
                "X-Dragonfly-Filtered-Query-Params",
                value.parse().map_err(|err| {
                    Error::InvalidArgument(format!("invalid filtered query params: {err}"))
                })?,
            );
        }

        headers.insert("X-Dragonfly-Use-P2P", HeaderValue::from_static("true"));
        Ok(headers)
    }
}

/// Implements helpers for the preheat feature.
impl Proxy {
    /// Helper function to check if a URL is an OCI blob URL (e.g., /v2/<name>/blobs/sha256:
    /// <digest>).
    #[cfg(feature = "preheat")]
    fn build_blob_url(registry: &str, repository: &str, digest: &str) -> String {
        format!("https://{registry}/v2/{repository}/blobs/{digest}")
    }

    /// Builds an OCI client with a platform resolver that matches the requested os/arch.
    #[cfg(feature = "preheat")]
    fn oci_client(platform: Option<String>) -> Result<OciClient> {
        let mut oci_config = ClientConfig::default();
        if let Some(platform) = platform {
            let (os, arch) = platform
                .split_once('/')
                .map(|(os, arch)| (Os::from(os), Arch::from(arch)))
                .ok_or_else(|| {
                    Error::InvalidArgument(format!("invalid platform format '{platform}', expected 'os/arch' (e.g., 'linux/amd64')"))
                })?;

            oci_config.platform_resolver = Some(Box::new(move |manifests: &[ImageIndexEntry]| {
                manifests
                    .iter()
                    .find(|entry| {
                        entry.platform.as_ref().is_some_and(|platform| {
                            platform.os == os && platform.architecture == arch
                        })
                    })
                    .map(|entry| entry.digest.clone())
            }))
        };

        Ok(OciClient::new(oci_config))
    }
}

/// The builder for ProxyWithEndpoints.
pub struct ProxyWithEndpointsBuilder {
    /// The seed peer endpoints serving the requests.
    endpoints: Vec<String>,

    /// The number of times to retry a request.
    max_retries: u8,
}

/// Implements Default trait.
impl Default for ProxyWithEndpointsBuilder {
    /// Returns a default ProxyWithEndpointsBuilder.
    fn default() -> Self {
        Self {
            endpoints: Vec::new(),
            max_retries: 1,
        }
    }
}

/// Implements the builder pattern for ProxyWithEndpoints.
impl ProxyWithEndpointsBuilder {
    /// Sets the seed peer endpoints serving the requests.
    pub fn endpoints(mut self, endpoints: Vec<String>) -> Self {
        self.endpoints = endpoints;
        self
    }

    /// Sets the maximum number of retries.
    pub fn max_retries(mut self, retries: u8) -> Self {
        self.max_retries = retries;
        self
    }

    /// Builds and returns a ProxyWithEndpoints instance.
    pub async fn build(self) -> Result<ProxyWithEndpoints> {
        // Validate input parameters.
        self.validate()?;

        // Create a client per endpoint, so every endpoint has its own reusable
        // connection pool.
        let factory = HTTPClientFactory::default();
        let mut clients = HashMap::with_capacity(self.endpoints.len());
        for endpoint in self.endpoints.iter() {
            if clients.contains_key(endpoint) {
                continue;
            }

            clients.insert(endpoint.clone(), factory.make_client(endpoint).await?);
        }

        Ok(ProxyWithEndpoints {
            endpoints: self.endpoints,
            max_retries: self.max_retries,
            clients,
        })
    }

    /// Validates the input parameters.
    fn validate(&self) -> Result<()> {
        if self.endpoints.is_empty() {
            return Err(Error::InvalidArgument(
                "endpoints must not be empty".to_string(),
            ));
        }

        if self.max_retries > 10 {
            return Err(Error::InvalidArgument(
                "max retries must be between 0 and 10".to_string(),
            ));
        }

        Ok(())
    }
}

/// The HTTP proxy client that sends requests via the fixed seed peer endpoints of the
/// Dragonfly given at construction, without selecting seed peers by the consistent hash
/// ring or syncing them from the scheduler.
#[derive(Clone)]
pub struct ProxyWithEndpoints {
    /// The seed peer endpoints serving the requests.
    endpoints: Vec<String>,

    /// The number of times to retry a request.
    max_retries: u8,

    /// The clients keyed by endpoint, so every endpoint has its own reusable
    /// connection pool.
    clients: HashMap<String, ClientWithMiddleware>,
}

/// Implements the proxy client that sends requests via the fixed seed peer endpoints.
impl ProxyWithEndpoints {
    /// Returns a new ProxyWithEndpointsBuilder for ProxyWithEndpoints.
    pub fn builder() -> ProxyWithEndpointsBuilder {
        ProxyWithEndpointsBuilder::default()
    }

    /// Scatters the request across the endpoints: it tries randomly picked endpoints
    /// one by one, limited by the max retries.
    async fn try_send(&self, request: &GetRequest) -> Result<reqwest::Response> {
        // Scatter the request across the endpoints: shuffle them and make
        // 1 + max retries attempts, wrapping around when the endpoints are
        // fewer than the attempts.
        let mut shuffled: Vec<&String> = self.endpoints.iter().collect();
        shuffled.shuffle(&mut rand::rng());

        let mut last_err = None;
        for attempt in 0..(self.max_retries as usize + 1) {
            let endpoint = shuffled[attempt % shuffled.len()];
            let client = self
                .clients
                .get(endpoint)
                .ok_or_else(|| Error::Internal(format!("no client for endpoint {endpoint}")))?;
            match self.send(client, request).await {
                Ok(response) => return Ok(response),
                Err(err) => {
                    warn!("failed to send request to endpoint {}: {:?}", endpoint, err);
                    last_err = Some(err);
                }
            }
        }

        Err(last_err.unwrap_or_else(|| {
            Error::Internal("failed to send request to any endpoint".to_string())
        }))
    }

    /// Send a request to the specified URL via the client with the given headers.
    async fn send(
        &self,
        client: &ClientWithMiddleware,
        request: &GetRequest,
    ) -> Result<reqwest::Response> {
        let headers = self.make_request_headers(request)?;
        let response = client
            .get(&request.url)
            .headers(headers)
            .timeout(request.timeout)
            .send()
            .await
            .map_err(|err| match err {
                reqwest_middleware::Error::Reqwest(err) if err.is_timeout() => {
                    Error::RequestTimeout(err.to_string())
                }
                err => Error::Internal(err.to_string()),
            })?;

        let status = response.status();
        if status.is_success() {
            return Ok(response);
        }

        let response_headers = response.headers().clone();
        let header_map = headermap_to_hashmap(&response_headers);
        let message = response.text().await.ok();
        let error_type = response_headers
            .get("X-Dragonfly-Error-Type")
            .and_then(|v| v.to_str().ok());

        match error_type {
            Some("backend") => Err(Error::BackendError(BackendError {
                message,
                header: header_map,
                status_code: Some(status),
            })),
            Some("proxy") => Err(Error::ProxyError(ProxyError {
                message,
                header: header_map,
                status_code: Some(status),
            })),
            Some("dfdaemon") => Err(Error::DfdaemonError(DfdaemonError { message })),
            Some(other) => Err(Error::ProxyError(ProxyError {
                message: Some(format!("unknown error type from proxy: {other}")),
                header: header_map,
                status_code: Some(status),
            })),
            None => Err(Error::ProxyError(ProxyError {
                message: Some(format!("unexpected status code from proxy: {status}")),
                header: header_map,
                status_code: Some(status),
            })),
        }
    }

    /// Make request headers applies p2p related headers to the request headers.
    fn make_request_headers(&self, request: &GetRequest) -> Result<HeaderMap> {
        let mut headers = request.header.clone();
        if let Some(piece_length) = request.piece_length {
            headers.insert(
                "X-Dragonfly-Piece-Length",
                piece_length.to_string().parse().map_err(|err| {
                    Error::InvalidArgument(format!("invalid piece length: {err}"))
                })?,
            );
        }

        if let Some(tag) = request.tag.clone() {
            headers.insert(
                "X-Dragonfly-Tag",
                tag.to_string()
                    .parse()
                    .map_err(|err| Error::InvalidArgument(format!("invalid tag: {err}")))?,
            );
        }

        if let Some(application) = request.application.clone() {
            headers.insert(
                "X-Dragonfly-Application",
                application
                    .to_string()
                    .parse()
                    .map_err(|err| Error::InvalidArgument(format!("invalid application: {err}")))?,
            );
        }

        if let Some(content_for_calculating_task_id) =
            request.content_for_calculating_task_id.clone()
        {
            headers.insert(
                "X-Dragonfly-Content-For-Calculating-Task-ID",
                content_for_calculating_task_id
                    .to_string()
                    .parse()
                    .map_err(|err| {
                        Error::InvalidArgument(format!(
                            "invalid content for calculating task id: {err}"
                        ))
                    })?,
            );
        }

        headers.insert(
            "X-Dragonfly-Enable-Task-ID-Based-Blob-Digest",
            request
                .enable_task_id_based_blob_digest
                .to_string()
                .parse()
                .map_err(|err| {
                    Error::InvalidArgument(format!(
                        "invalid enable task id based blob digest: {err}"
                    ))
                })?,
        );

        if let Some(priority) = request.priority {
            headers.insert(
                "X-Dragonfly-Priority",
                priority
                    .to_string()
                    .parse()
                    .map_err(|err| Error::InvalidArgument(format!("invalid priority: {err}")))?,
            );
        }

        if !request.filtered_query_params.is_empty() {
            let value = request.filtered_query_params.join(",");
            headers.insert(
                "X-Dragonfly-Filtered-Query-Params",
                value.parse().map_err(|err| {
                    Error::InvalidArgument(format!("invalid filtered query params: {err}"))
                })?,
            );
        }

        headers.insert("X-Dragonfly-Use-P2P", HeaderValue::from_static("true"));
        Ok(headers)
    }
}

/// Implements the interface for sending requests via the fixed seed peer endpoints of the
/// Dragonfly given at construction, without selecting seed peers by the consistent hash
/// ring or syncing them from the scheduler.
#[async_trait]
impl RequestWithEndpoints for ProxyWithEndpoints {
    /// Sends an GET request to a remote server via the seed peer endpoints of the
    /// Dragonfly and returns a response with a streaming body. The request is sent to a
    /// randomly picked endpoint and retried on the others up to the max retries.
    async fn get(&self, request: &GetRequest) -> Result<GetResponse> {
        request.validate()?;

        let get = async {
            let response = self.try_send(request).await?;
            let header = response.headers().clone();
            let status_code = response.status();
            let body: Body = Box::new(
                response
                    .bytes_stream()
                    .map_err(|err| Error::Internal(err.to_string())),
            );

            Ok(GetResponse {
                success: status_code.is_success(),
                header,
                status_code: Some(status_code),
                body: Some(body),
            })
        };

        tokio::time::timeout(request.timeout, get)
            .await
            .map_err(|err| Error::RequestTimeout(err.to_string()))?
    }

    /// Sends an GET request to a remote server via the seed peer endpoints of the
    /// Dragonfly and writes the response body directly into the provided buffer. The
    /// request is sent to a randomly picked endpoint and retried on the others up to the
    /// max retries.
    async fn get_into(&self, request: &GetRequest, buf: &mut BytesMut) -> Result<GetResponse> {
        request.validate()?;

        let get_into = async {
            let mut response = self.try_send(request).await?;
            let status = response.status();
            let header = response.headers().clone();

            if status.is_success() {
                // Reserve the capacity upfront and copy each chunk into the buffer
                // directly, without aggregating the whole body first.
                if let Some(content_length) = response.content_length() {
                    buf.reserve(content_length as usize);
                }

                while let Some(chunk) = response.chunk().await.map_err(|err| {
                    if err.is_timeout() {
                        return Error::RequestTimeout(err.to_string());
                    }

                    Error::Internal(format!("failed to read response body: {err}"))
                })? {
                    buf.extend_from_slice(&chunk);
                }
            }

            Ok(GetResponse {
                success: status.is_success(),
                header,
                status_code: Some(status),
                body: None,
            })
        };

        tokio::time::timeout(request.timeout, get_into)
            .await
            .map_err(|err| Error::RequestTimeout(err.to_string()))?
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::Request;
    use dragonfly_api::common::v2::Host;
    use dragonfly_api::dfdaemon::v2::DownloadTaskResponse;
    use dragonfly_api::scheduler::v2::ListHostsResponse;
    use mocktail::prelude::*;
    use std::time::Duration;
    use tonic_health::pb::health_check_response::ServingStatus;
    use tonic_health::pb::HealthCheckResponse;

    async fn setup_mock_scheduler(hosts: Vec<Host>) -> Result<mocktail::server::MockServer> {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.path("/scheduler.v2.Scheduler/ListHosts");
            then.pb(ListHostsResponse { hosts });
        });

        let server = MockServer::new_grpc("scheduler.v2.Scheduler").with_mocks(mocks);
        server.start().await.map_err(|err| {
            Error::Internal(format!("failed to start mock scheduler server: {err}"))
        })?;

        Ok(server)
    }

    async fn setup_mock_seed_peer(mut mocks: MockSet) -> Result<mocktail::server::MockServer> {
        mocks.mock(|when, then| {
            when.path("/grpc.health.v1.Health/Check");
            then.pb(HealthCheckResponse {
                status: ServingStatus::Serving as i32,
            });
        });

        let server = MockServer::new_grpc("dfdaemon.v2.DfdaemonUpload").with_mocks(mocks);
        server.start().await.map_err(|err| {
            Error::Internal(format!("failed to start mock seed peer server: {err}"))
        })?;

        Ok(server)
    }

    fn create_seed_peer_host(name: &str, port: u16, proxy_port: u16) -> Host {
        Host {
            id: name.to_string(),
            r#type: 1,
            hostname: name.to_string(),
            ip: "127.0.0.1".to_string(),
            port: port as i32,
            proxy_port: proxy_port as i32,
            name: name.to_string(),
            ..Default::default()
        }
    }

    async fn setup_mock_seed_peer_proxy(mocks: MockSet) -> Result<mocktail::server::MockServer> {
        let server = MockServer::new_http("seed-peer-proxy").with_mocks(mocks);
        server.start().await.map_err(|err| {
            Error::Internal(format!("failed to start mock seed peer proxy: {err}"))
        })?;

        Ok(server)
    }

    #[tokio::test]
    async fn test_new_success() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let result = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await;

        assert!(result.is_ok());
        assert_eq!(result.unwrap().max_retries, 1);
    }

    #[tokio::test]
    async fn test_new_empty_endpoint() {
        let result = Proxy::builder()
            .scheduler_endpoint("".to_string())
            .build()
            .await;

        assert!(result.is_err());
        assert!(matches!(result, Err(Error::InvalidArgument(_))));
    }

    #[tokio::test]
    async fn test_new_invalid_retry_times() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let result = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .max_retries(11)
            .build()
            .await;

        assert!(result.is_err());
        assert!(matches!(result, Err(Error::InvalidArgument(_))));
    }

    #[tokio::test]
    async fn test_new_invalid_health_check_interval() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let result = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .max_retries(11)
            .build()
            .await;

        assert!(result.is_err());
        assert!(matches!(result, Err(Error::InvalidArgument(_))));
    }

    #[tokio::test]
    async fn test_preheat_no_available_seed_peers() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = PreheatRequest {
            url: "http://example.com/payload.txt".to_string(),
            tag: Some("preheat".to_string()),
            application: Some("dfctl".to_string()),
            replicas: 1,
            ..Default::default()
        };

        let result = proxy.preheat(&request).await;
        assert!(
            matches!(result, Err(Error::Internal(message)) if message.contains("failed to select seed peers"))
        );
    }

    #[tokio::test]
    async fn test_preheat_succeeds_with_seed_peer() {
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.path("/dfdaemon.v2.DfdaemonUpload/DownloadTask");
            then.pb_stream(vec![
                DownloadTaskResponse {
                    host_id: "seed-peer-1".to_string(),
                    task_id: "task-1".to_string(),
                    peer_id: "peer-1".to_string(),
                    ..Default::default()
                },
                DownloadTaskResponse {
                    host_id: "seed-peer-1".to_string(),
                    task_id: "task-1".to_string(),
                    peer_id: "peer-1".to_string(),
                    ..Default::default()
                },
            ]);
        });

        let mock_seed_peer = setup_mock_seed_peer(mocks).await.unwrap();
        let mock_scheduler = setup_mock_scheduler(vec![create_seed_peer_host(
            "seed-peer-1",
            mock_seed_peer.port().unwrap(),
            0,
        )])
        .await
        .unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = PreheatRequest {
            url: "http://example.com/payload.txt".to_string(),
            tag: Some("preheat".to_string()),
            application: Some("dfctl".to_string()),
            replicas: 1,
            ..Default::default()
        };

        let result = proxy.preheat(&request).await;
        assert!(result.is_ok(), "preheat should succeed: {:?}", result.err());
    }

    #[tokio::test]
    async fn test_preheat_insufficient_seed_peers() {
        let mock_seed_peer = setup_mock_seed_peer(MockSet::new()).await.unwrap();
        let mock_scheduler = setup_mock_scheduler(vec![create_seed_peer_host(
            "seed-peer-1",
            mock_seed_peer.port().unwrap(),
            0,
        )])
        .await
        .unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = PreheatRequest {
            url: "http://example.com/payload.txt".to_string(),
            ..Default::default()
        };

        let result = proxy.preheat(&request).await;
        assert!(
            matches!(result, Err(Error::Internal(message)) if message.contains("insufficient seed peers"))
        );
    }

    #[tokio::test]
    async fn test_get() {
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.text("hello dragonfly");
        });
        let mock_proxy = setup_mock_seed_peer_proxy(mocks).await.unwrap();

        let mock_seed_peer = setup_mock_seed_peer(MockSet::new()).await.unwrap();
        let mock_scheduler = setup_mock_scheduler(vec![create_seed_peer_host(
            "seed-peer-1",
            mock_seed_peer.port().unwrap(),
            mock_proxy.port().unwrap(),
        )])
        .await
        .unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            replicas: 1,
            ..Default::default()
        };

        let response = proxy.get(&request).await.unwrap();
        assert!(response.success);
        assert_eq!(response.status_code, Some(reqwest::StatusCode::OK));

        let mut body = response.body.unwrap();
        let mut content = Vec::new();
        while let Some(chunk) = body.try_next().await.unwrap() {
            content.extend_from_slice(&chunk);
        }
        assert_eq!(content, b"hello dragonfly");
    }

    #[tokio::test]
    async fn test_get_into() {
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.text("hello dragonfly");
        });
        let mock_proxy = setup_mock_seed_peer_proxy(mocks).await.unwrap();

        let mock_seed_peer = setup_mock_seed_peer(MockSet::new()).await.unwrap();
        let mock_scheduler = setup_mock_scheduler(vec![create_seed_peer_host(
            "seed-peer-1",
            mock_seed_peer.port().unwrap(),
            mock_proxy.port().unwrap(),
        )])
        .await
        .unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            replicas: 1,
            ..Default::default()
        };

        let mut buf = BytesMut::new();
        let response = proxy.get_into(&request, &mut buf).await.unwrap();
        assert!(response.success);
        assert_eq!(response.status_code, Some(reqwest::StatusCode::OK));
        assert!(response.body.is_none());
        assert_eq!(&buf[..], b"hello dragonfly");
    }

    #[tokio::test]
    async fn test_get_scatters_across_replicas() {
        let mut bad_mocks = MockSet::new();
        bad_mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.status(reqwest::StatusCode::INTERNAL_SERVER_ERROR)
                .text("boom");
        });
        let bad_proxy = setup_mock_seed_peer_proxy(bad_mocks).await.unwrap();

        let mut good_mocks = MockSet::new();
        good_mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.text("ok");
        });
        let good_proxy = setup_mock_seed_peer_proxy(good_mocks).await.unwrap();

        let seed_peer_1 = setup_mock_seed_peer(MockSet::new()).await.unwrap();
        let seed_peer_2 = setup_mock_seed_peer(MockSet::new()).await.unwrap();
        let mock_scheduler = setup_mock_scheduler(vec![
            create_seed_peer_host(
                "seed-peer-1",
                seed_peer_1.port().unwrap(),
                bad_proxy.port().unwrap(),
            ),
            create_seed_peer_host(
                "seed-peer-2",
                seed_peer_2.port().unwrap(),
                good_proxy.port().unwrap(),
            ),
        ])
        .await
        .unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            ..Default::default()
        };

        let mut buf = BytesMut::new();
        let response = proxy.get_into(&request, &mut buf).await.unwrap();
        assert!(response.success);
        assert_eq!(&buf[..], b"ok");
    }

    #[tokio::test]
    async fn test_get_error_type_backend() {
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.status(reqwest::StatusCode::INTERNAL_SERVER_ERROR)
                .headers([("X-Dragonfly-Error-Type", "backend")])
                .text("boom");
        });
        let mock_proxy = setup_mock_seed_peer_proxy(mocks).await.unwrap();

        let mock_seed_peer = setup_mock_seed_peer(MockSet::new()).await.unwrap();
        let mock_scheduler = setup_mock_scheduler(vec![create_seed_peer_host(
            "seed-peer-1",
            mock_seed_peer.port().unwrap(),
            mock_proxy.port().unwrap(),
        )])
        .await
        .unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            replicas: 1,
            ..Default::default()
        };

        let result = proxy.get(&request).await;
        assert!(
            matches!(result, Err(Error::BackendError(err)) if err.message.as_deref() == Some("boom"))
        );
    }

    #[tokio::test]
    async fn test_new_with_endpoints_empty() {
        let result = ProxyWithEndpoints::builder()
            .endpoints(vec![])
            .build()
            .await;
        assert!(matches!(result, Err(Error::InvalidArgument(_))));
    }

    #[tokio::test]
    async fn test_get_into_with_endpoints() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.text("hello dragonfly");
        });
        let mock_proxy = setup_mock_seed_peer_proxy(mocks).await.unwrap();

        let endpoints = vec![
            "http://127.0.0.1:1".to_string(),
            format!("http://127.0.0.1:{}", mock_proxy.port().unwrap()),
        ];
        let proxy = ProxyWithEndpoints::builder()
            .endpoints(endpoints)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            ..Default::default()
        };

        let mut buf = BytesMut::new();
        let response = proxy.get_into(&request, &mut buf).await.unwrap();
        assert!(response.success);
        assert_eq!(response.status_code, Some(reqwest::StatusCode::OK));
        assert_eq!(&buf[..], b"hello dragonfly");
    }

    #[tokio::test]
    async fn test_new_with_endpoints() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let endpoints = vec![
            "http://127.0.0.1:4001".to_string(),
            "http://127.0.0.1:4001".to_string(),
            "http://127.0.0.1:4002".to_string(),
        ];
        let proxy = ProxyWithEndpoints::builder()
            .endpoints(endpoints.clone())
            .build()
            .await
            .unwrap();

        assert_eq!(proxy.max_retries, 1);
        assert_eq!(proxy.endpoints, endpoints);
        assert_eq!(proxy.clients.len(), 2);
        assert!(proxy.clients.contains_key("http://127.0.0.1:4001"));
        assert!(proxy.clients.contains_key("http://127.0.0.1:4002"));
    }

    #[tokio::test]
    async fn test_new_with_endpoints_invalid_max_retries() {
        let result = ProxyWithEndpoints::builder()
            .endpoints(vec!["http://127.0.0.1:4001".to_string()])
            .max_retries(11)
            .build()
            .await;
        assert!(matches!(result, Err(Error::InvalidArgument(_))));
    }

    #[tokio::test]
    async fn test_new_with_endpoints_invalid_endpoint() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let result = ProxyWithEndpoints::builder()
            .endpoints(vec!["://".to_string()])
            .build()
            .await;
        assert!(matches!(result, Err(Error::Internal(_))));
    }

    #[tokio::test]
    async fn test_get_with_endpoints() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.text("hello dragonfly");
        });
        let mock_proxy = setup_mock_seed_peer_proxy(mocks).await.unwrap();

        let endpoints = vec![
            "http://127.0.0.1:1".to_string(),
            format!("http://127.0.0.1:{}", mock_proxy.port().unwrap()),
        ];
        let proxy = ProxyWithEndpoints::builder()
            .endpoints(endpoints)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            ..Default::default()
        };

        let response = proxy.get(&request).await.unwrap();
        assert!(response.success);
        assert_eq!(response.status_code, Some(reqwest::StatusCode::OK));

        let mut body = response.body.unwrap();
        let mut content = Vec::new();
        while let Some(chunk) = body.try_next().await.unwrap() {
            content.extend_from_slice(&chunk);
        }
        assert_eq!(content, b"hello dragonfly");
    }

    #[tokio::test]
    async fn test_get_with_endpoints_all_endpoints_down() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let proxy = ProxyWithEndpoints::builder()
            .endpoints(vec!["http://127.0.0.1:1".to_string()])
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            ..Default::default()
        };

        let result = proxy.get(&request).await;
        assert!(matches!(result, Err(Error::Internal(_))));
    }

    #[tokio::test]
    async fn test_get_with_endpoints_invalid_replicas() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let proxy = ProxyWithEndpoints::builder()
            .endpoints(vec!["http://127.0.0.1:4001".to_string()])
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            replicas: 0,
            ..Default::default()
        };

        let result = proxy.get(&request).await;
        assert!(matches!(result, Err(Error::InvalidArgument(_))));
    }

    #[tokio::test]
    async fn test_get_with_endpoints_error_type_backend() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.get().path("/file.txt");
            then.status(reqwest::StatusCode::INTERNAL_SERVER_ERROR)
                .headers([("X-Dragonfly-Error-Type", "backend")])
                .text("boom");
        });
        let mock_proxy = setup_mock_seed_peer_proxy(mocks).await.unwrap();

        let proxy = ProxyWithEndpoints::builder()
            .endpoints(vec![format!(
                "http://127.0.0.1:{}",
                mock_proxy.port().unwrap()
            )])
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            ..Default::default()
        };

        let result = proxy.get(&request).await;
        assert!(
            matches!(result, Err(Error::BackendError(err)) if err.message.as_deref() == Some("boom"))
        );
    }

    #[tokio::test]
    async fn test_get_invalid_replicas() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/file.txt".to_string(),
            replicas: 0,
            ..Default::default()
        };

        let result = proxy.get(&request).await;
        assert!(matches!(result, Err(Error::InvalidArgument(_))));
    }

    #[tokio::test]
    async fn test_preheat_and_get_hit_same_seed_peers() {
        let cases = [
            ("http://example.com/replicas-1.txt", 1, vec!["seed-peer-1"]),
            (
                "http://example.com/replicas-2.txt",
                2,
                vec!["seed-peer-1", "seed-peer-2"],
            ),
            (
                "http://example.com/replicas-3.txt",
                3,
                vec!["seed-peer-1", "seed-peer-2", "seed-peer-3"],
            ),
        ];

        for (url, replicas, expected) in cases {
            let path = url.strip_prefix("http://example.com").unwrap();
            let mut hosts = Vec::new();
            let mut servers = Vec::new();
            let mut proxy_servers = Vec::new();
            for name in ["seed-peer-1", "seed-peer-2", "seed-peer-3"] {
                let mut mocks = MockSet::new();
                if expected.contains(&name) {
                    mocks.mock(|when, then| {
                        when.path("/dfdaemon.v2.DfdaemonUpload/DownloadTask");
                        then.pb_stream(vec![DownloadTaskResponse {
                            host_id: name.to_string(),
                            task_id: "task-1".to_string(),
                            peer_id: "peer-1".to_string(),
                            ..Default::default()
                        }]);
                    });
                }
                let seed_peer = setup_mock_seed_peer(mocks).await.unwrap();

                let mut proxy_mocks = MockSet::new();
                proxy_mocks.mock(|when, then| {
                    when.get().path(path);
                    then.status(reqwest::StatusCode::INTERNAL_SERVER_ERROR)
                        .text(name);
                });
                let seed_peer_proxy = setup_mock_seed_peer_proxy(proxy_mocks).await.unwrap();

                hosts.push(create_seed_peer_host(
                    name,
                    seed_peer.port().unwrap(),
                    seed_peer_proxy.port().unwrap(),
                ));
                servers.push(seed_peer);
                proxy_servers.push((name, seed_peer_proxy));
            }

            let mock_scheduler = setup_mock_scheduler(hosts).await.unwrap();
            let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());

            let proxy = Proxy::builder()
                .scheduler_endpoint(scheduler_endpoint)
                .max_retries(2)
                .build()
                .await
                .unwrap();

            let preheat_request = PreheatRequest {
                url: url.to_string(),
                replicas,
                ..Default::default()
            };
            proxy.preheat(&preheat_request).await.unwrap();

            let request = GetRequest {
                url: url.to_string(),
                replicas,
                ..Default::default()
            };

            let mut buf = BytesMut::new();
            let result = proxy.get_into(&request, &mut buf).await;
            assert!(result.is_err());

            for (name, server) in proxy_servers.iter() {
                let hits: usize = server.mocks().iter().map(|mock| mock.match_count()).sum();
                if expected.contains(name) {
                    assert!(hits >= 1, "{url}: expected seed peer {name} to be hit");
                } else {
                    assert_eq!(hits, 0, "{url}: unexpected seed peer {name} was hit");
                }
            }
        }
    }

    #[tokio::test]
    async fn test_lookup_endpoints() {
        let mut servers = Vec::new();
        let mut endpoints = std::collections::HashMap::new();
        let mut hosts = Vec::new();
        for name in ["seed-peer-1", "seed-peer-2", "seed-peer-3"] {
            let server = setup_mock_seed_peer(MockSet::new()).await.unwrap();
            let port = server.port().unwrap();
            endpoints.insert(name, format!("http://127.0.0.1:{port}"));
            hosts.push(create_seed_peer_host(name, port, 0));
            servers.push(server);
        }

        let mock_scheduler = setup_mock_scheduler(hosts).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let blob_url = "http://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cce7e68013d5441c6326d5a3e1b12aeb5ed58564d0fd3fa089bc29cb6e";
        let cases = [
            (
                GetRequest {
                    url: "https://example.com/file.txt?Expires=e1&Signature=s1&foo=bar".to_string(),
                    piece_length: Some(4194304),
                    tag: Some("tag-a".to_string()),
                    application: Some("app-a".to_string()),
                    filtered_query_params: vec!["Expires".to_string(), "Signature".to_string()],
                    replicas: 3,
                    ..Default::default()
                },
                vec!["seed-peer-3", "seed-peer-2", "seed-peer-1"],
            ),
            (
                GetRequest {
                    url: "https://example.com/file.txt?Expires=e2&Signature=s2&foo=bar".to_string(),
                    piece_length: Some(4194304),
                    tag: Some("tag-a".to_string()),
                    application: Some("app-a".to_string()),
                    filtered_query_params: vec!["Expires".to_string(), "Signature".to_string()],
                    replicas: 3,
                    ..Default::default()
                },
                vec!["seed-peer-3", "seed-peer-2", "seed-peer-1"],
            ),
            (
                GetRequest {
                    url: "https://example.com/file.txt".to_string(),
                    ..Default::default()
                },
                vec!["seed-peer-1", "seed-peer-2"],
            ),
            (
                GetRequest {
                    url: "https://example.com/file.txt".to_string(),
                    replicas: 1,
                    ..Default::default()
                },
                vec!["seed-peer-1"],
            ),
            (
                GetRequest {
                    url: "https://example.com/file.txt".to_string(),
                    tag: Some("tag-a".to_string()),
                    ..Default::default()
                },
                vec!["seed-peer-3", "seed-peer-2"],
            ),
            (
                GetRequest {
                    url: "https://example.com/file.txt".to_string(),
                    tag: Some("tag-b".to_string()),
                    ..Default::default()
                },
                vec!["seed-peer-2", "seed-peer-1"],
            ),
            (
                GetRequest {
                    url: "https://example.com/file.txt".to_string(),
                    application: Some("app-a".to_string()),
                    ..Default::default()
                },
                vec!["seed-peer-1", "seed-peer-3"],
            ),
            (
                GetRequest {
                    url: "https://example.com/file.txt".to_string(),
                    content_for_calculating_task_id: Some("This is a test file".to_string()),
                    replicas: 3,
                    ..Default::default()
                },
                vec!["seed-peer-2", "seed-peer-3", "seed-peer-1"],
            ),
            (
                GetRequest {
                    url: blob_url.to_string(),
                    replicas: 3,
                    ..Default::default()
                },
                vec!["seed-peer-3", "seed-peer-2", "seed-peer-1"],
            ),
            (
                GetRequest {
                    url: blob_url.to_string(),
                    enable_task_id_based_blob_digest: false,
                    ..Default::default()
                },
                vec!["seed-peer-3", "seed-peer-1"],
            ),
        ];

        for (request, expected_names) in cases {
            let expected: Vec<String> = expected_names
                .iter()
                .map(|name| endpoints[name].clone())
                .collect();
            assert_eq!(proxy.lookup_endpoints(&request).await.unwrap(), expected);
        }
    }

    #[tokio::test]
    async fn test_lookup_endpoints_no_available_seed_peers() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = GetRequest {
            url: "http://example.com/payload.txt".to_string(),
            ..Default::default()
        };

        let result = proxy.lookup_endpoints(&request).await;
        assert!(
            matches!(result, Err(Error::Internal(message)) if message.contains("failed to select seed peers"))
        );
    }

    #[tokio::test]
    async fn test_preheat_fails_when_seed_peer_download_fails() {
        let mut mocks = MockSet::new();
        mocks.mock(|when, then| {
            when.path("/dfdaemon.v2.DfdaemonUpload/DownloadTask");
            then.error(StatusCode::INTERNAL_SERVER_ERROR, "storage is full");
        });

        let mock_seed_peer = setup_mock_seed_peer(mocks).await.unwrap();
        let mock_scheduler = setup_mock_scheduler(vec![create_seed_peer_host(
            "seed-peer-1",
            mock_seed_peer.port().unwrap(),
            0,
        )])
        .await
        .unwrap();

        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_scheduler.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = PreheatRequest {
            url: "http://example.com/payload.txt".to_string(),
            tag: Some("preheat".to_string()),
            application: Some("dfctl".to_string()),
            replicas: 1,
            ..Default::default()
        };

        let result = proxy.preheat(&request).await;
        assert!(
            matches!(result, Err(Error::Internal(message)) if message.contains("failed to download task"))
        );
    }

    #[cfg(feature = "preheat")]
    #[tokio::test]
    async fn test_preheat_image_invalid_reference() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = PreheatImageRequest {
            image: "invalid image reference!!".to_string(),
            ..Default::default()
        };

        let result = proxy.preheat_image(&request).await;
        assert!(
            matches!(result, Err(Error::InvalidArgument(message)) if message.contains("invalid image reference"))
        );
    }

    #[cfg(feature = "preheat")]
    #[tokio::test]
    async fn test_preheat_image_invalid_platform() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = PreheatImageRequest {
            image: "docker.io/library/nginx:latest".to_string(),
            platform: Some("linux-amd64".to_string()),
            ..Default::default()
        };

        let result = proxy.preheat_image(&request).await;
        assert!(
            matches!(result, Err(Error::InvalidArgument(message)) if message.contains("invalid platform format"))
        );
    }

    #[cfg(feature = "preheat")]
    #[tokio::test]
    async fn test_preheat_image_unreachable_registry() {
        let mock_server = setup_mock_scheduler(vec![]).await.unwrap();
        let scheduler_endpoint = format!("http://0.0.0.0:{}", mock_server.port().unwrap());
        let proxy = Proxy::builder()
            .scheduler_endpoint(scheduler_endpoint)
            .build()
            .await
            .unwrap();

        let request = PreheatImageRequest {
            image: "127.0.0.1:1/library/nginx:latest".to_string(),
            ..Default::default()
        };

        let result = proxy.preheat_image(&request).await;
        assert!(
            matches!(result, Err(Error::Internal(message)) if message.contains("failed to pull image manifest"))
        );
    }

    #[cfg(feature = "preheat")]
    #[test]
    fn test_build_blob_url_uses_https_by_default() {
        let url = Proxy::build_blob_url("registry.example.com", "library/nginx", "sha256:abcdef");

        assert_eq!(
            url,
            "https://registry.example.com/v2/library/nginx/blobs/sha256:abcdef"
        );
    }

    #[tokio::test]
    async fn test_client_pool_get_or_create() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let pool = PoolBuilder::new(HTTPClientFactory {})
            .capacity(10)
            .idle_timeout(Duration::from_secs(600))
            .build();

        assert_eq!(pool.size().await, 0);

        let addr = "http://proxy1.com".to_string();
        let _ = pool.entry(&addr, &addr).await.unwrap();
        assert_eq!(pool.size().await, 1);

        let _ = pool.entry(&addr, &addr).await.unwrap();
        assert_eq!(pool.size().await, 1);

        let addr = "http://proxy2.com".to_string();
        let _ = pool.entry(&addr, &addr).await.unwrap();
        assert_eq!(pool.size().await, 2);
    }

    #[tokio::test]
    async fn test_client_pool_cleanup() {
        let _ = rustls::crypto::aws_lc_rs::default_provider().install_default();
        let pool = PoolBuilder::new(HTTPClientFactory {})
            .capacity(10)
            .idle_timeout(Duration::from_millis(10))
            .build();

        let addr = "http://proxy1.com".to_string();
        let _ = pool.entry(&addr, &addr).await.unwrap();
        assert_eq!(pool.size().await, 1);
        tokio::time::sleep(Duration::from_millis(50)).await;

        let addr = "http://proxy2.com".to_string();
        let _ = pool.entry(&addr, &addr).await.unwrap();
        assert_eq!(pool.size().await, 1);
    }
}
