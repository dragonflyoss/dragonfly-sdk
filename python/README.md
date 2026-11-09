# Dragonfly Python SDK

Python SDK for `dfget`: download models and datasets through a local
[dfdaemon](https://d7y.io) with the same request semantics as the `dfget` CLI.

The SDK is a thin synchronous client over dfdaemon's `DownloadTask` gRPC
service. dfdaemon performs the download (using Dragonfly's P2P network where
possible) and writes the file to the requested output path.

## Requirements

- Python 3.9 or newer
- A running `dfdaemon`, reachable through its Unix domain socket:
  - Linux: `/var/run/dragonfly/dfdaemon.sock`
  - macOS: `~/.dragonfly/dfdaemon.sock`

## Installation

```shell
pip install dragonfly-sdk
```

## Quick start

```python
from dragonfly_sdk import Client

with Client() as client:
    result = client.download(
        "https://example.com/model.bin",
        "./model.bin",
    )
    print(result.content_length, result.elapsed)
```

On Linux, if the socket lives elsewhere, pass it explicitly:

```python
client = Client(endpoint="/var/run/dragonfly/dfdaemon.sock")
```

## Supported sources

| Source                | URL scheme                                    | Credentials class         |
| --------------------- | --------------------------------------------- | ------------------------- |
| HTTP(S)               | `http://`, `https://`                         | —                         |
| S3-compatible storage | `s3://`, `gs://`, `abs://`, `oss://`, `obs://`, `cos://` | `ObjectStorageCredentials` |
| HuggingFace Hub       | `hf://`                                       | `HuggingFaceCredentials`  |
| ModelScope            | `modelscope://`                               | `ModelScopeCredentials`   |
| OpenCSG               | `opencsg://`                                  | `OpenCSGCredentials`      |

### HuggingFace

```python
from dragonfly_sdk import Client, HuggingFaceCredentials

with Client() as client:
    result = client.download(
        "hf://Qwen/Qwen2.5-7B/model.safetensors",
        "./model.safetensors",
        credentials=HuggingFaceCredentials(token="hf_...", revision="main"),
    )
```

### ModelScope / OpenCSG

```python
from dragonfly_sdk import Client, ModelScopeCredentials, OpenCSGCredentials

with Client() as client:
    client.download(
        "modelscope://Qwen/Qwen2.5-7B/model.safetensors",
        "./model.safetensors",
        credentials=ModelScopeCredentials(token="...", revision="master"),
    )
    client.download(
        "opencsg://owner/repo/model.safetensors",
        "./other.safetensors",
        credentials=OpenCSGCredentials(token="...", revision="main"),
    )
```

### S3-compatible object storage

```python
from dragonfly_sdk import Client, ObjectStorageCredentials

with Client() as client:
    client.download(
        "s3://my-bucket/datasets/train.parquet",
        "./train.parquet",
        credentials=ObjectStorageCredentials(
            region="us-east-1",
            access_key_id="...",
            access_key_secret="...",
        ),
    )
```

When no credentials are passed, dfdaemon uses the default credential chain for
the storage backend.

## Progress

```python
from dragonfly_sdk import Client


def on_progress(progress):
    print(
        f"{progress.percent:5.1f}% "
        f"{progress.completed_pieces}/{progress.total_pieces} pieces "
        f"{progress.speed_bytes_per_second / 1e6:.1f} MB/s"
    )


with Client() as client:
    client.download(
        "https://example.com/dataset.tar",
        "./dataset.tar",
        on_progress=on_progress,
    )
```

The callback runs on the thread that called `download`; progress is reported
once when the task starts and once per finished piece.

## Common options

```python
client.download(
    url,
    output,
    headers={"Authorization": "Bearer ..."},  # or ["Name: value", ...]
    digest="sha256:...",  # verify the downloaded file
    tag="my-app",  # task grouping
    priority=6,  # 0 (lowest) .. 6 (highest)
    timeout=7200,  # seconds, or a timedelta
    overwrite=True,  # replace an existing file
    disable_back_to_source=True,  # do not fall back to origin
    scheduling_policy="auto",  # or "always"
)
```

## Errors

All exceptions derive from `dragonfly_sdk.DragonflyError`:

```
DragonflyError
├── ValidationError            (also a ValueError)
├── ConnectionFailedError
├── ClientClosedError
└── DownloadFailedError
    ├── DfdaemonError          # code, message, details
    ├── BackendError           # message, headers, status_code
    ├── IncompleteDownloadError# missing_pieces, total_pieces
    ├── DeadlineExceededError  (also a TimeoutError)
    └── DownloadCancelledError
```

```python
from dragonfly_sdk import BackendError, Client, DownloadFailedError

try:
    with Client() as client:
        client.download("s3://bucket/missing.bin", "./missing.bin")
except BackendError as exc:
    print(exc.status_code, exc.message)
except DownloadFailedError as exc:
    print("download failed:", exc)
```

## Limitations

- Single-file downloads only; dfget's directory mode is not supported yet.
- HDFS sources are not supported yet.
- The SDK does not transfer piece content; dfdaemon writes the output file.
- A failed download may leave a partial file at the output path.
- Only a local Unix domain socket is supported; dfdaemon is expected on the
  same host.

## Development

The package supports Python 3.9 and newer, but the development tooling
(ruff, mypy, build, twine) requires Python 3.10+:

```shell
python -m venv .venv
. .venv/bin/activate
pip install -e '.[dev]'
ruff check . && ruff format --check .
mypy src
pytest -q -m "not integration"
```

To run the tests on Python 3.9, install only the `test` extra:

```shell
pip install -e '.[test]'
```

A live end-to-end test is available when a dfdaemon is running:

```shell
DRAGONFLY_LIVE_TEST=1 pytest -q -m integration
```

Set `DRAGONFLY_DFDAEMON_SOCKET` to point the live test at a non-default socket.

## License

Apache 2.0. See [LICENSE](./LICENSE).
