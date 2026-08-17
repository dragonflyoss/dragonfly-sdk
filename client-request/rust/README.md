# dragonfly-client-request

[![Crates.io](https://img.shields.io/crates/v/dragonfly-client-request.svg)](https://crates.io/crates/dragonfly-client-request)
[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client. It sends requests to remote servers
via the Dragonfly P2P network, supporting streaming and buffered GET requests
and preheating files or OCI images through seed peers.

## Usage

```rust
use dragonfly_client_request::{GetRequest, Proxy, Request};

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
```

Look up the endpoints of the seed peers serving a request, then download
directly from a specific endpoint, skipping the hash ring selection:

```rust
let request = GetRequest {
    url: "https://example.com/file.txt".to_string(),
    ..Default::default()
};

let endpoints = proxy.lookup_endpoints(&request).await?;
let response = proxy.get_with_endpoint(&endpoints[0], &request).await?;

// Or write the response body directly into a buffer:
// let response = proxy.get_into_with_endpoint(&endpoints[0], &request, &mut buf).await?;
```

The `preheat` feature enables preheating OCI images by resolving manifests from
the registry and triggering seed peers to download each blob:

```toml
[dependencies]
dragonfly-client-request = { version = "1.5.0", features = ["preheat"] }
```

See [examples](./examples) for runnable examples.

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
