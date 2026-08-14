# client-request (Go)

[![Go Reference](https://pkg.go.dev/badge/d7y.io/dragonfly-sdk/client-request/go.svg)](https://pkg.go.dev/d7y.io/dragonfly-sdk/client-request/go)
[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client. It sends requests to remote servers
via the Dragonfly P2P network, supporting streaming and buffered GET requests
and preheating files or OCI images through seed peers.

It is the Go implementation of the Rust crate
[dragonfly-client-request](../rust) and generates identical task ids and seed
peer selections, pinned by shared cross-language test vectors.

## Install

```console
go get d7y.io/dragonfly-sdk/client-request/go
```

## Usage

```go
import (
    "context"

    request "d7y.io/dragonfly-sdk/client-request/go"
)

func main() {
    ctx := context.Background()
    proxy, err := request.New(ctx, "http://127.0.0.1:8002")
    if err != nil {
        panic(err)
    }
    defer proxy.Close()

    resp, err := proxy.Get(ctx, request.NewGetRequest("https://example.com/file.txt"))
    if err != nil {
        panic(err)
    }
    defer resp.Body.Close()
    // Read resp.Body...
}
```

Optional request parameters are set with `With*` options:

```go
req := request.NewGetRequest(
    "https://example.com/file.txt",
    request.WithGetRequestTag("tag"),
    request.WithGetRequestApplication("app"),
    request.WithGetRequestTimeout(30*time.Second),
)
```

Preheat a file or an OCI image to the seed peers:

```go
if err := proxy.Preheat(ctx, request.NewPreheatRequest("https://example.com/file.txt")); err != nil {
    panic(err)
}

if err := proxy.PreheatImage(ctx, request.NewPreheatImageRequest("docker.io/library/nginx:latest")); err != nil {
    panic(err)
}
```

See [examples](./examples) for runnable examples.

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
