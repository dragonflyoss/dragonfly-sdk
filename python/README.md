# Dragonfly Python SDK

Python SDK for [Dragonfly](https://d7y.io), providing a wrapper over
the `dfget` download client for developers.

It talks to a locally running `dfdaemon` over its download gRPC service and is built on 
the generated bindings published as the
[`dragonfly-api`](https://pypi.org/project/dragonfly-api/) package.

## Requirements

- Python >= 3.9
- A running `dfdaemon` (part of the [Dragonfly client](https://github.com/dragonflyoss/client))

## Installation

```bash
pip install dragonfly-sdk
```

## Usage

```python
from dragonfly_sdk import Client

# Connects to /var/run/dragonfly/dfdaemon.sock by default.
with Client() as client:
    result = client.download(
        "https://example.com/file.tar.gz",
        "/tmp/file.tar.gz",
        tag="my-app",
        application="my-app",
    )
    print(result.task_id, result.content_length)
```

## Development

```bash
cd python
pip install -e ".[test]"
pytest
```
