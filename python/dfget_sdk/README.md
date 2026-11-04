# dragonfly-dfget-sdk

Zero-dependency Python SDK for the [`dfget`](https://d7y.io/docs/next/reference/commands/client/dfget/)
command-line tool — pull models and datasets through
[Dragonfly](https://d7y.io)'s P2P network from Python.

This package wraps the locally installed `dfget` binary via `subprocess`. It has
**no third-party runtime dependencies**, works on Python >= 3.8, and is designed
for batch jobs, CI pipelines, and environments where the dfdaemon gRPC API is
not reachable. It implements [dragonflyoss/dragonfly#4421](https://github.com/dragonflyoss/dragonfly/issues/4421).

## Requirements

- Python >= 3.8
- A local [`dfget`](https://github.com/dragonflyoss/client) binary on `PATH`
  (or pointed to via `DFGET_BIN`)
- A running `dfdaemon` — `dfget` talks to its local daemon (unix socket) to
  perform the actual P2P download

## Install

```bash
pip install dragonfly-dfget-sdk
# or, from this repository:
cd python/dfget_sdk && pip install .
```

## Quickstart

```python
from dragonfly_dfget import DfgetClient

client = DfgetClient()
result = client.download(
    "huggingface://Qwen/Qwen2.5-7B/config.json",
    "./config.json",
)
print(result.output_path)   # absolute path of the downloaded file
print(result.task_id)       # Dragonfly task id, parsed from the CLI output
print(result.duration_s)    # wall-clock duration of the run
```

## API

### `DfgetClient(config=None, *, env=None)`

| Method | Description |
| --- | --- |
| `download(url, output, *, ...)` | Run `dfget` synchronously, return a `DownloadResult`. |
| `adownload(url, output, *, ...)` | Async twin using `asyncio` subprocesses. |
| `version()` | Output of `dfget --version` (stripped string). |
| `health()` | `HealthResult`: binary present **and** `--version` succeeds. Never raises for a missing binary. |
| `build_download_argv(url, output, *, ...)` | Expose the exact argv that would be executed. |

`download` / `adownload` keyword arguments (mirroring the real `dfget` flags):

| Keyword | dfget flag | Notes |
| --- | --- | --- |
| `overwrite=False` | `--overwrite` | overwrite an existing output file |
| `force_hard_link=False` | `--force-hard-link` | require hard-linking to the output path |
| `recursive=False` | `--recursive` (`-r`) | download directories recursively |
| `timeout="2h"` | `--timeout` | humantime string (`45s`, `30m`, `2h`); `None` falls back to `config.default_timeout` |
| `digest=None` | `--digest` | integrity check, e.g. `sha256:...`, `crc32:...` |
| `transfer_from_dfdaemon=False` | `--transfer-from-dfdaemon` | stream content from dfdaemon over its unix socket |
| `content_for_calculating_task_id=None` | `--content-for-calculating-task-id` | override how the task id is computed |
| `tag=None` | `--tag` | separates tasks for the same URL |
| `application=None` | `--application` | separates tasks for the same URL |
| `priority=None` | `--priority` (`-p`) | scheduling priority (dfget default: 6) |
| `background=None` | `--background` | passed through for dfget builds that support it |
| `extra_args=None` | *(verbatim)* | list of raw argv fragments appended last |
| `timeout_s=None` | *(not a dfget flag)* | **local** subprocess timeout in seconds; raises `DfgetTimeoutError` on expiry |

`--console` is always passed so that task ids, sizes and progress lines are
printed on stdout and can be parsed.

### `DownloadResult`

```python
@dataclass
class DownloadResult:
    output_path: str            # absolute path
    task_id: Optional[str]      # parsed from CLI output when printed
    raw_stdout: str             # untouched captured stdout
    raw_stderr: str             # untouched captured stderr
    duration_s: float           # wall-clock subprocess duration
    returncode: int = 0
    downloaded_size: Optional[int] = None   # parsed bytes, when printed
    progress_lines: Tuple[str, ...] = ()    # lines containing "42%" etc.
    argv: Tuple[str, ...] = ()
```

### Async usage

```python
import asyncio
from dragonfly_dfget import DfgetClient

async def main():
    client = DfgetClient()
    result = await client.adownload(
        "huggingface://Qwen/Qwen2.5-7B/config.json",
        "./config.json",
        timeout_s=600,          # give up locally after 10 minutes
    )
    print(result.task_id)

asyncio.run(main())
```

## Configuration & environment variables

`DfgetConfig` is a frozen dataclass — share it freely and derive variants with
`dataclasses.replace(config, ...)` (or `config.replace(...)`).

```python
from dataclasses import replace
from dragonfly_dfget import DfgetClient, DfgetConfig

config = DfgetConfig(binary="/opt/dragonfly/bin/dfget", default_timeout="30m")
client = DfgetClient(config)
quiet = DfgetClient(replace(config, log_level="warn"))
```

Binary path resolution precedence:

1. explicit `DfgetConfig(binary=...)`
2. `DFGET_BIN` environment variable
3. `PATH` lookup of `dfget`

| Environment variable | Meaning | Default |
| --- | --- | --- |
| `DFGET_BIN` | path to the dfget binary | — (then `PATH`) |
| `DFDAEMON_ENDPOINT` | dfdaemon endpoint passed as `-e` | dfget's own default (unix socket) |
| `DFGET_TIMEOUT` | default `--timeout` value | `2h` |
| `DFGET_LOG_LEVEL` | default `--log-level` value | unset (flag omitted) |

An explicit constructor value always beats the environment variable.

## Error handling

```python
from dragonfly_dfget import (
    DfgetError,             # base class — catch this to handle everything
    DfgetNotFoundError,     # dfget binary missing (install / DFGET_BIN / PATH)
    DfgetTimeoutError,      # local timeout_s budget elapsed
    DfgetCommandError,      # dfget exited non-zero
)

try:
    client.download(url, out)
except DfgetCommandError as exc:
    print(exc.returncode, exc.stderr, exc.argv)   # structured context
except DfgetError as exc:
    print(exc)
```

`health()` is the non-raising variant: it returns
`HealthResult(healthy=False, ...)` when the binary is missing or
`dfget --version` fails.

## Command-line interface

The package installs a `dfget-sdk` console script:

```bash
dfget-sdk download huggingface://Qwen/Qwen2.5-7B/config.json \
    -O ./config.json --json
dfget-sdk version --json
dfget-sdk health --json          # exit code 0 = healthy, 1 = unhealthy
```

Every subcommand supports `--json` for machine-readable output
(`{"ok": true, ...}` / `{"ok": false, "error": {...}}`), plus `--dfget-bin`
and `--endpoint` overrides.

## How it differs from the gRPC-based `dragonfly-sdk` package

Two complementary approaches exist for [#4421](https://github.com/dragonflyoss/dragonfly/issues/4421):

- **`dragonfly_sdk` (PR #87)** speaks gRPC directly to dfdaemon's
  `DownloadTask` service. Richer control and streaming progress events, but it
  needs the gRPC endpoint, generated protobuf stubs, and third-party runtime
  dependencies (`grpcio`, `protobuf`).
- **`dragonfly-dfget-sdk` (this package)** wraps the `dfget` CLI through
  `subprocess`. Zero runtime dependencies, trivially auditable, and it works
  anywhere a `dfget` binary runs — batch jobs, CI, minimal containers — at the
  cost of parsing textual output instead of receiving structured events.

If you already run dfdaemon with gRPC reachable, prefer `dragonfly_sdk`;
otherwise this package is the portable option.

## Limitations

- Requires a local `dfget` binary (from the
  [Dragonfly client](https://github.com/dragonflyoss/client)) — this SDK does
  not implement any download protocol itself.
- Requires a running `dfdaemon`; `dfget` delegates every download to its local
  daemon. Without it, `dfget` exits non-zero and `download()` raises
  `DfgetCommandError`.
- `task_id` / `downloaded_size` / `progress_lines` are parsed from textual CLI
  output; when dfget does not print them, the fields are `None` / empty.
- `--background` is forwarded verbatim for dfget builds that accept it; the
  current Rust `dfget` does not define this flag.
- Verification of this SDK is subprocess-mock-driven (unit tests with `pytest`);
  no live cluster is exercised by the test-suite.

## Development

```bash
cd python/dfget_sdk
uv run --with pytest --with pytest-asyncio python -m pytest tests -q
uv run python -m compileall -q src
```

All tests mock every subprocess call — no real `dfget` binary, cluster, or
network access is needed.

## License

Apache-2.0
