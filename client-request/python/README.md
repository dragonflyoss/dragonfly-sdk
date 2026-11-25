# client-request (Python)

[![LICENSE](https://img.shields.io/github/license/dragonflyoss/dragonfly-sdk.svg?style=flat-square)](https://github.com/dragonflyoss/dragonfly-sdk/blob/main/LICENSE)

Request library for the Dragonfly client, the Python implementation of the Rust
crate [dragonfly-client-request](https://github.com/dragonflyoss/dragonfly-sdk/tree/main/client-request/rust)
and the Go module [client-request](https://github.com/dragonflyoss/dragonfly-sdk/tree/main/client-request/go).

The package is being ported in steps. It currently has the error types the
request surface raises, the rule for which failures are retried against
another seed peer, and the url canonicalization the task id is computed from.
The task id generation and the seed peer hash ring come next, then GET, stat,
preheat and delete.

The scheduler canonicalizes urls with Go's `net/url`, so this package does the
same. Where the Rust url crate applies a WHATWG normalization the scheduler
does not (lowercasing the host, dropping a default port, resolving dot
segments), the url is kept as written, like the scheduler. A url the
scheduler rejects raises `InvalidArgumentError`.

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
