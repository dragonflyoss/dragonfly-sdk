# dragonfly-client-request

[![Crates.io](https://img.shields.io/crates/v/dragonfly-client-request.svg)](https://crates.io/crates/dragonfly-client-request)
[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client. It sends requests to remote servers
via the Dragonfly P2P network, supporting streaming and buffered GET requests,
preheating files or OCI images through seed peers and deleting preheated files
or OCI images from them.

## Usage

```rust
use dragonfly_client_request::{Proxy, GetRequest, Request};
use futures::TryStreamExt;

let proxy = Proxy::builder()
    .scheduler_endpoint("http://127.0.0.1:8002".to_string())
    .build()
    .await?;

let response = proxy
    .get(&GetRequest {
        url: "https://example.com/file.txt".to_string(),
        ..Default::default()
    })
    .await?;

// The body is a stream of zero-copy `Bytes` chunks.
let mut body = response.body.unwrap();
while let Some(chunk) = body.try_next().await? {
    // Consume the chunk...
}
```

Preheat with multiple replicas and scatter downloads across them. Preheating
writes the file to the given number of distinct seed peers, and downloading
scatters each request across those replicas by picking a random one, retrying
on the others up to the max retries. Preheating fails when fewer of the task's
replicas are healthy than requested, while downloading uses its healthy
replicas whose proxies are not ejected, failing when none is. The default
replicas is 2:

```rust
proxy
    .preheat(&PreheatRequest {
        url: "https://example.com/file.txt".to_string(),
        replicas: 3,
        ..Default::default()
    })
    .await?;

let response = proxy
    .get(&GetRequest {
        url: "https://example.com/file.txt".to_string(),
        replicas: 3,
        ..Default::default()
    })
    .await?;
```

Preheat to all seed peers regardless of the replicas with the all seed peers
scope, aligned with the `all_seed_peers` scope of the manager preheat job, so
any seed peer a download picks holds the file. Delete with the same scope to
remove the file from every seed peer:

```rust
proxy
    .preheat(&PreheatRequest {
        url: "https://example.com/file.txt".to_string(),
        scope: Scope::AllSeedPeers,
        ..Default::default()
    })
    .await?;

proxy
    .delete(&DeleteRequest {
        url: "https://example.com/file.txt".to_string(),
        scope: Scope::AllSeedPeers,
        ..Default::default()
    })
    .await?;
```

Retry transient failures. A request is retried on another seed peer when the
peer cannot be reached or answers too slowly, or the proxy, backend or dfdaemon
answers `5xx`, `408` or `429`, since another seed peer may not be rate limited or
out of space. Any other answer, such as `404` or a dfdaemon `422`, is definitive
and returns at once. A body that breaks off is retried too: `get_into` reads it
again from the start, and `get` sends the request again within the same retries,
its `Range` narrowed to the bytes not streamed yet. Preheating retries the same
way on each replica seed peer.

The default is one retry at once and `max_retries(0)` disables retries. An
exponential backoff spreads the retries out, `ExponentialBuilder` being
re-exported from [backon](https://docs.rs/backon). The request timeout applies
to each attempt:

```rust
use dragonfly_client_request::ExponentialBuilder;
use std::time::Duration;

let proxy = Proxy::builder()
    .scheduler_endpoint("http://127.0.0.1:8002".to_string())
    .max_retries(3)
    .backoff(
        ExponentialBuilder::new()
            .with_min_delay(Duration::from_millis(100))
            .with_max_delay(Duration::from_secs(1))
            .with_jitter(),
    )
    .build()
    .await?;
```

Look up the endpoints of the seed peers serving a request, then create a proxy
bound to those endpoints and download from them directly, scattering the
request across them. The endpoints proxy keeps a client with a reusable
connection pool per endpoint and doesn't sync seed peers from the scheduler:

```rust
let request = GetRequest {
    url: "https://example.com/file.txt".to_string(),
    ..Default::default()
};

let endpoints = proxy.lookup_endpoints(&request).await?;
let proxy_with_endpoints = ProxyWithEndpoints::builder()
    .endpoints(endpoints)
    .build()
    .await?;

let response = proxy_with_endpoints.get(&request).await?;

// Or write the response body directly into a buffer:
// let response = proxy_with_endpoints.get_into(&request, &mut buf).await?;
```

Share the seed peers across processes, such as several clients on a host, so
only one of them lists the seed peers from the scheduler and checks their
health. `SchedulerClient::list_seed_peers` returns every seed peer with its
health, and a proxy built from them neither connects to the scheduler nor
refreshes them, which `update_seed_peers` does instead. The consistent hash
ring is built from every seed peer, healthy or not, so a task stays on its
replicas: an unhealthy replica is skipped, the request failing when every
replica is, and serves the task again once it recovers. A seed peer proxy
refusing the connection or unreachable 5 times in a row is skipped for 30
seconds:

```rust
use dragonfly_client_request::{ProxyWithSeedPeers, SchedulerClient};

let scheduler_client = SchedulerClient::new("http://127.0.0.1:8002", Duration::from_secs(5)).await?;
let seed_peers = scheduler_client.list_seed_peers().await?;
let proxy = ProxyWithSeedPeers::builder()
    .seed_peers(seed_peers)
    .build()
    .await?;

// Later, replace them with the ones listed again, such as by another process.
let seed_peers = scheduler_client.list_seed_peers().await?;
proxy.update_seed_peers(seed_peers).await;
```

Since 1.11.0 a proxy built from the scheduler endpoint selects the same way, a
breaking change: a task whose replicas are all unhealthy fails rather than
moving to another seed peer, and a seed peer answering its health check with
`NOT_SERVING` counts as unhealthy.

The `preheat` feature enables preheating OCI images by resolving manifests from
the registry and triggering seed peers to download each blob, querying the
distribution of an OCI image with the layers cached by each seed peer, and
deleting a preheated OCI image from the seed peers. An image is preheated for
all its platforms unless a platform is set, and the seed peers record its tasks
by the image and the platform:

```toml
[dependencies]
dragonfly-client-request = { version = "1.13.0", features = ["preheat"] }
```

```rust
proxy
    .preheat_image(&PreheatImageRequest {
        image: "docker.io/library/nginx:latest".to_string(),
        ..Default::default()
    })
    .await?;

let response = proxy
    .stat_image(&StatImageRequest {
        image: "docker.io/library/nginx:latest".to_string(),
        ..Default::default()
    })
    .await?;
```

Delete a preheated file or an OCI image from the seed peers, `delete_image`
needing the `preheat` feature. A file delete should carry the parameters the
preheat used, such as the replicas and the scope, so it addresses the same task
and seed peers, and a seed peer answering `NotFound` counts as deleted. An image
is deleted from all healthy seed peers, for all its platforms unless a platform
is set, and a task still referenced by another image is retained:

```rust
proxy
    .delete(&DeleteRequest {
        url: "https://example.com/file.txt".to_string(),
        ..Default::default()
    })
    .await?;

let response = proxy
    .delete_image(&DeleteImageRequest {
        image: "docker.io/library/nginx:latest".to_string(),
        ..Default::default()
    })
    .await?;

for peer in response.peers {
    println!(
        "peer {} ({}) deleted {} tasks and retained {} tasks",
        peer.hostname,
        peer.ip,
        peer.deleted_task_ids.len(),
        peer.retained_task_ids.len()
    );
}
```

Since 1.13.0 a breaking change: `preheat_image` preheats all the platforms
unless a platform is set, `delete_image` takes only the platform and the timeout
and returns the tasks deleted and retained, and `PeerImage` is renamed to
`PeerStatedImage`. The seed peers must run dfdaemon v1.5.12 or later.

See [examples](./examples) for runnable examples.

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
