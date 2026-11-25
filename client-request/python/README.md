# client-request (Python)

[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client, the Python implementation of the Rust
crate [dragonfly-client-request](https://github.com/dragonflyoss/dragonfly-sdk/tree/main/client-request/rust)
and the Go module [client-request](https://github.com/dragonflyoss/dragonfly-sdk/tree/main/client-request/go).

The package is being ported in steps. It currently generates the same task ids
and seed peer selections as the Go and Rust SDKs, pinned by the shared
cross-language test vectors. GET, stat, preheat and delete come next.

## Usage

Generate the task id of a file. The signed query parameters of the object
storages are filtered out before hashing, so every presigned url of the same
object maps to the same task:

```python
from dragonfly_client_request import DEFAULT_FILTERED_QUERY_PARAMS, task_id

id = task_id(
    "https://bucket.s3.amazonaws.com/models/llama/model.safetensors?X-Amz-Signature=...",
    filtered_query_params=DEFAULT_FILTERED_QUERY_PARAMS,
)
```

Optional parameters take part in the task id the same way they do in the
other SDKs:

```python
id = task_id(
    "https://example.com/file.txt",
    piece_length=4194304,
    tag="tag",
    application="app",
    filtered_query_params=["Expires", "Signature"],
)
```

An OCI blob url, or a manifest url referenced by a digest, uses the digest as
the task id, and a content string hashes the content instead of the url:

```python
id = task_id("https://registry.example.com/v2/library/ubuntu/blobs/sha256:b2c366cc...")

id = task_id("", content_for_calculating_task_id="This is a test file")
```

Select the seed peers serving a task with the consistent hash ring the
scheduler and the client use. The ring returns the virtual node owning the
task first and the next ones clockwise. Neighboring virtual nodes can belong
to the same seed peer, so skip repeated names the way the Go selector does:

```python
from dragonfly_client_request import VNodeHashRing

ring = VNodeHashRing()
for seed_peer in ["seed-peer-1", "seed-peer-2", "seed-peer-3"]:
    ring.add(seed_peer)

vnodes = ring.get_with_replicas(id, replicas=len(ring))
seed_peers = list(dict.fromkeys(vnode.name for vnode in vnodes))[:2]
```

## Compatibility

The scheduler canonicalizes urls with Go's `net/url`, so this package does the
same. Where the Rust url crate applies a WHATWG normalization the scheduler
does not (lowercasing the host, dropping a default port, resolving dot
segments), the url is kept as written, like the scheduler. A url the scheduler
rejects raises `InvalidArgumentError` instead of hashing an empty url, and a
manifest url referenced by an `md5`, `sha1` or `blake3` digest falls back to
the url based task id, like the Rust client.

## Development

```console
cd client-request/python
uv sync
uv run ruff check
uv run mypy
uv run pytest
```

## Documentation

You can find the full documentation on [d7y.io](https://d7y.io).

## LICENSE

Apache 2.0 License. Please see [LICENSE](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE) for more information.
