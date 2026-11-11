# client-request (Go)

[![Go Reference](https://pkg.go.dev/badge/d7y.io/dragonfly-sdk/client-request/go.svg)](https://pkg.go.dev/d7y.io/dragonfly-sdk/client-request/go)
[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client. It sends requests to remote servers
via the Dragonfly P2P network, supporting streaming and buffered GET requests,
preheating files or OCI images through seed peers and deleting preheated files
or OCI images from them.

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

Retry transient failures. A request is retried on another seed peer when the
peer cannot be reached or answers too slowly, or the proxy, backend or dfdaemon
answers `5xx`, `408` or `429`, since another seed peer may not be rate limited
or out of space. Any other answer, such as `404` or a dfdaemon `422`, is
definitive and returns at once. A body that breaks off is retried too: `Get`
and `GetInto` send the request again within the same retries, its `Range`
narrowed to the bytes not read yet, so the writer of `GetInto` is never
rewound. That needs the response to tell its length, by a `Content-Length` or
the `Content-Range` of a single range; a body without them, such as a chunked
one, fails when it breaks off. Preheating retries the same way on each replica
seed peer.

The default is one retry at once and `WithProxyMaxRetries(0)` disables retries.
An exponential backoff from [cenkalti/backoff](https://pkg.go.dev/github.com/cenkalti/backoff/v5)
spreads the retries out, every request starting from a fresh copy of it. The
request timeout applies to each attempt:

```go
import "github.com/cenkalti/backoff/v5"

proxy, err := request.New(ctx, "http://127.0.0.1:8002",
    request.WithProxyMaxRetries(3),
    request.WithProxyBackoff(backoff.NewExponentialBackOff()),
)
```

Preheat a file or an OCI image to the seed peers. An image is preheated for all
its platforms unless a platform is set, and the seed peers record its tasks by
the image and the platform:

```go
if err := proxy.Preheat(ctx, request.NewPreheatRequest("https://example.com/file.txt")); err != nil {
    panic(err)
}

if err := proxy.PreheatImage(ctx, request.NewPreheatImageRequest("docker.io/library/nginx:latest")); err != nil {
    panic(err)
}
```

Query the distribution of an OCI image, listing the layers cached by each
seed peer:

```go
resp, err := proxy.StatImage(ctx, request.NewStatImageRequest("docker.io/library/nginx:latest"))
if err != nil {
    panic(err)
}

fmt.Printf("image has %d layers\n", len(resp.Layers))
for _, peer := range resp.Peers {
    fmt.Printf("peer %s (%s) cached %d layers\n", peer.Hostname, peer.IP, len(peer.CachedLayers))
}
```

Delete a preheated file or an OCI image from the seed peers. A file delete
should carry the parameters the preheat used, such as the replicas and the
scope, so it addresses the same task and seed peers, and a seed peer answering
`NotFound` counts as deleted. An image is deleted from all healthy seed peers,
for all its platforms unless a platform is set, a task still referenced by
another image or platform is retained, and a seed peer that fails is left out
of the response:

```go
if err := proxy.Delete(ctx, request.NewDeleteRequest("https://example.com/file.txt")); err != nil {
    panic(err)
}

resp, err := proxy.DeleteImage(ctx, request.NewDeleteImageRequest("docker.io/library/nginx:latest"))
if err != nil {
    panic(err)
}

for _, peer := range resp.Peers {
    fmt.Printf("peer %s (%s) deleted %d tasks and retained %d tasks\n", peer.Hostname, peer.IP, len(peer.DeletedTaskIDs), len(peer.RetainedTaskIDs))
}
```

Since 1.13.0 a breaking change: `PreheatImage` preheats all the platforms unless
a platform is set, `StatImage` resolves the image itself and asks the seed peers
instead of the scheduler, `DeleteImage` no longer takes the options of the
preheat and returns the tasks deleted and retained, and `PeerImage` is renamed
to `PeerStatedImage`. The seed peers must run dfdaemon v1.5.12 or later.

Preheat with multiple replicas and scatter downloads across them. Preheating
writes the file to the given number of distinct seed peers, and downloading
scatters each request across those replicas by picking a random one, retrying
on the others up to the max retries. Preheating fails when fewer of the task's
replicas are healthy than requested, while downloading uses its healthy
replicas whose proxies are not ejected, failing when none is. The default
replicas is 2:

```go
if err := proxy.Preheat(ctx, request.NewPreheatRequest("https://example.com/file.txt", request.WithPreheatRequestReplicas(3))); err != nil {
    panic(err)
}

resp, err := proxy.Get(ctx, request.NewGetRequest("https://example.com/file.txt", request.WithGetRequestReplicas(3)))
if err != nil {
    panic(err)
}
defer resp.Body.Close()
```

Preheat to all seed peers regardless of the replicas with the all seed peers
scope, aligned with the `all_seed_peers` scope of the manager preheat job, so
any seed peer a download picks holds the file. Delete with the same scope to
remove the file from every seed peer:

```go
if err := proxy.Preheat(ctx, request.NewPreheatRequest("https://example.com/file.txt", request.WithPreheatRequestScope(request.ScopeAllSeedPeers))); err != nil {
    panic(err)
}

if err := proxy.PreheatImage(ctx, request.NewPreheatImageRequest("docker.io/library/nginx:latest", request.WithPreheatImageRequestScope(request.ScopeAllSeedPeers))); err != nil {
    panic(err)
}

if err := proxy.Delete(ctx, request.NewDeleteRequest("https://example.com/file.txt", request.WithDeleteRequestScope(request.ScopeAllSeedPeers))); err != nil {
    panic(err)
}
```

Look up the endpoints of the seed peers serving a request, then create a proxy
bound to those endpoints and download from them directly, scattering the
request across them. The endpoints proxy keeps a client with a reusable
connection pool per endpoint and doesn't sync seed peers from the scheduler:

```go
req := request.NewGetRequest("https://example.com/file.txt")
endpoints, err := proxy.LookupEndpoints(ctx, req)
if err != nil {
    panic(err)
}

proxyWithEndpoints, err := request.NewWithEndpoints(endpoints)
if err != nil {
    panic(err)
}

resp, err := proxyWithEndpoints.Get(ctx, req)
if err != nil {
    panic(err)
}
defer resp.Body.Close()

// Or write the response body directly into a writer:
// resp, err := proxyWithEndpoints.GetInto(ctx, req, w)
```

Share the seed peers across processes, such as several clients on a host, so
only one of them lists the seed peers from the scheduler and checks their
health. `SchedulerClient.ListSeedPeers` returns every seed peer with its
health, and a proxy created with `NewWithSeedPeers` neither connects to the
scheduler nor refreshes them, which `UpdateSeedPeers` does instead. The
consistent hash ring is built from every seed peer, healthy or not, so a task
stays on its replicas: an unhealthy replica is skipped, the request failing
when every replica is, and serves the task again once it recovers. A seed peer
proxy refusing the connection or unreachable 5 times in a row is skipped for
30 seconds:

```go
schedulerClient, err := request.NewSchedulerClient("http://127.0.0.1:8002", 5*time.Second)
if err != nil {
    panic(err)
}
defer schedulerClient.Close()

seedPeers, err := schedulerClient.ListSeedPeers(ctx)
if err != nil {
    panic(err)
}

proxy, err := request.NewWithSeedPeers(seedPeers)
if err != nil {
    panic(err)
}

// Later, replace them with the ones listed again, such as by another process.
seedPeers, err = schedulerClient.ListSeedPeers(ctx)
if err != nil {
    panic(err)
}
proxy.UpdateSeedPeers(seedPeers)
```

Since 1.11.0 a proxy created with the scheduler endpoint selects the same way, a
breaking change: a task whose replicas are all unhealthy fails rather than
moving to another seed peer, and a seed peer answering its health check with
`NOT_SERVING` counts as unhealthy.

See [examples](./examples) for runnable examples.

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
