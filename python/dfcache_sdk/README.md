# dragonfly-dfcache — Python SDK for Dragonfly's persistent cache

A lightweight, **unofficial** Python client for [Dragonfly](https://d7y.io)'s
persistent cache (`dfcache`), implemented per
[dragonflyoss/dragonfly#4422](https://github.com/dragonflyoss/dragonfly/issues/4422).
It wraps the `dfcache` command-line tool via `subprocess`, so the SDK has
**zero third-party runtime dependencies** and does not require a running
Dragonfly cluster to import, configure or test.

```text
python/dfcache_sdk/
├── pyproject.toml
├── README.md
├── src/dragonfly_dfcache/
│   ├── __init__.py     # public API exports
│   ├── client.py       # DfcacheClient (sync + asyncio wrappers)
│   ├── config.py       # DfcacheConfig (endpoint/tls/timeout/binary)
│   ├── errors.py       # typed exception hierarchy
│   ├── cli.py          # `dfcache-sdk` console entry point
│   └── _version.py
└── tests/              # pytest suite (all subprocess calls mocked)
```

## Installation

```bash
pip install dragonfly-dfcache
```

Or from a checkout of this repository:

```bash
cd python/dfcache_sdk
pip install .
```

The SDK needs the `dfcache` binary available on `PATH` (or an explicit path
via configuration) at call time. Install Dragonfly's client to get it.

## Quickstart

```python
from dragonfly_dfcache import DfcacheClient

client = DfcacheClient()

# Import a local file into the persistent cache and get a task ID.
result = client.import_file("/data/model.bin", persistent_replica_count=2)
task_id = result.get("task_id")

# Query cached metadata for the task.
meta = client.stat(task_id)

# Export the cached content to a local path.
client.export(task_id, "/tmp/model.bin")

# Remove it from the cache when done.
client.delete(task_id)

# Availability probes (never raise).
print(client.version())
print(client.health())   # True/False
```

Prefer the command line? The package also installs a `dfcache-sdk` entry
point that mirrors the same operations over JSON output:

```bash
dfcache-sdk --endpoint http://manager:65001 stat <task-id>
dfcache-sdk import ./big.bin --persistent-replica-count 2
dfcache-sdk export <task-id> --output ./out.bin
dfcache-sdk delete <task-id>
dfcache-sdk version
dfcache-sdk health
```

## Configuration

All knobs live on `DfcacheConfig`. Precedence, highest first:

1. **Explicit arguments** passed when constructing `DfcacheConfig(...)`
   (and per-call `timeout=...` on every client method).
2. **Environment variables** — `DFCACHE_ENDPOINT`, `DFCACHE_TIMEOUT`,
   `DFCACHE_TLS`, `DFCACHE_TLS_CA_CERT`, `DFCACHE_BINARY`.
3. **Defaults** — `http://localhost:8080`, 30s timeout, TLS off, and a
   `dfcache` lookup on `PATH`.

```python
from dragonfly_dfcache import DfcacheClient, DfcacheConfig

config = DfcacheConfig(
    endpoint="http://manager:65001",   # dfdaemon/manager endpoint flag
    timeout=60.0,                      # subprocess timeout in seconds
    tls=True,
    tls_ca_cert="/etc/ca.pem",
    binary_path="/usr/local/bin/dfcache",
)
client = DfcacheClient(config)

# Per-call override beats config:
meta = client.stat(task_id, timeout=5.0)
```

## Error handling

Every failure surfaces as a `DfcacheError` subclass:

| Exception | Meaning |
| --- | --- |
| `DfcacheNotFoundError` | `dfcache` binary missing or not executable |
| `DfcacheTimeoutError`  | CLI invocation exceeded its timeout |
| `DfcacheCommandError`  | CLI exited non-zero; carries `exit_code`, `stdout`, `stderr` |

CLI output is parsed leniently: JSON blobs are preferred, with a
`key = value` fallback, so both machine- and human-readable dfcache output
yield a `dict`.

```python
from dragonfly_dfcache import (
    DfcacheCommandError,
    DfcacheError,
    DfcacheNotFoundError,
    DfcacheTimeoutError,
)

try:
    client.import_file("/data/model.bin")
except DfcacheNotFoundError:
    ...  # dfcache not installed / wrong path
except DfcacheTimeoutError:
    ...  # retry with a larger timeout
except DfcacheCommandError as exc:
    print(exc.exit_code, exc.stderr)
except DfcacheError:
    ...  # catch-all for any SDK failure
```

## Async usage

Each sync method has an asyncio-native twin (`stat`/`astat`,
`import_file`/`aimport`, `export`/`aexport`, `delete`/`adelete`,
`version`/`aversion`, `health`/`ahealth`) built on `asyncio` subprocesses:

```python
import asyncio
from dragonfly_dfcache import DfcacheClient

async def main():
    client = DfcacheClient()
    print(await client.ahealth())
    result = await client.aimport("/data/model.bin", persistent_replica_count=2)
    print(await client.astat(result["task_id"], timeout=5.0))
    await client.aexport(result["task_id"], "/tmp/model.bin")
    await client.adelete(result["task_id"])

asyncio.run(main())
```

## Running the tests

The suite mocks `subprocess.run` and `asyncio.create_subprocess_exec`;
no real `dfcache` binary or Dragonfly cluster is needed.

```bash
cd python/dfcache_sdk
uv run --with pytest --with pytest-asyncio python -m pytest tests -q

# Byte-compile check
uv run python -m compileall -q src
```

## License

Apache-2.0, matching the Dragonfly project.
