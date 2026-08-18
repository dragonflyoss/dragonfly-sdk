# dragonfly-client-request

[![Crates.io](https://img.shields.io/crates/v/dragonfly-client-request.svg)](https://crates.io/crates/dragonfly-client-request)
[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client. It sends requests to remote servers
via the Dragonfly P2P network, supporting streaming and buffered GET requests
and preheating files or OCI images through seed peers.

## Usage

```rust
use dragonfly_client_request::{Builder, Client, GetRequest};
use futures::TryStreamExt;

let client = Builder::default()
    .scheduler_endpoint("http://127.0.0.1:8002".to_string())
    .build()
    .await?;

let response = client
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
client
    .preheat(&PreheatRequest {
        url: "https://example.com/file.txt".to_string(),
        replicas: 3,
        ..Default::default()
    })
    .await?;

let response = client
    .get(&GetRequest {
        url: "https://example.com/file.txt".to_string(),
        replicas: 3,
        ..Default::default()
    })
    .await?;
```

Look up the endpoints of the seed peers serving a request, then download from
the looked-up endpoints directly with a `ClientWithEndpoints`, scattering each
request across them. The client with endpoints never connects to the
scheduler, so the endpoints can also be provided by an external system:

```rust
use dragonfly_client_request::{BuilderWithEndpoints, ClientWithEndpoints};

let request = GetRequest {
    url: "https://example.com/file.txt".to_string(),
    ..Default::default()
};

let endpoints = client.lookup_endpoints(&request).await?;
let client_with_endpoints = BuilderWithEndpoints::default()
    .endpoints(endpoints)
    .build()?;
let response = client_with_endpoints.get(&request).await?;

// Or write the response body directly into a buffer:
// let response = client_with_endpoints.get_into(&request, &mut buf).await?;
```

The `preheat` feature enables preheating OCI images by resolving manifests from
the registry and triggering seed peers to download each blob:

```toml
[dependencies]
dragonfly-client-request = { version = "1.5.1", features = ["preheat"] }
```

See [examples](./examples) for runnable examples.

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
