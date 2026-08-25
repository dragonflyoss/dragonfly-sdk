# dragonfly-client-request

[![Crates.io](https://img.shields.io/crates/v/dragonfly-client-request.svg)](https://crates.io/crates/dragonfly-client-request)
[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client. It sends requests to remote servers
via the Dragonfly P2P network, supporting streaming and buffered GET requests
and preheating files or OCI images through seed peers.

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
on the others up to the max retries. Preheating fails when the available seed
peers are fewer than the replicas, while downloading clamps the replicas to
the available seed peers. The default replicas is 2:

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

The `preheat` feature enables preheating OCI images by resolving manifests from
the registry and triggering seed peers to download each blob, and querying the
distribution of an OCI image with the layers cached by each seed peer:

```toml
[dependencies]
dragonfly-client-request = { version = "1.6.1", features = ["preheat"] }
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

See [examples](./examples) for runnable examples.

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
