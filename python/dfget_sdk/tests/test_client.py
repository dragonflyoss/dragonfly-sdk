"""Client tests: exact argv construction, result parsing, error mapping.

Every subprocess call is mocked - no real dfget binary is executed.
"""

from __future__ import annotations

import asyncio
import os
import subprocess

import pytest

from dragonfly_dfget import (
    DfgetClient,
    DfgetConfig,
    DownloadResult,
    HealthResult,
    __version__,
)
from dragonfly_dfget.errors import (
    DfgetCommandError,
    DfgetError,
    DfgetNotFoundError,
    DfgetTimeoutError,
)

URL = "https://example.com/model.safetensors"
OUTPUT = "./model.safetensors"


def make_client(**config_kwargs) -> DfgetClient:
    """Client with an explicit binary and an empty env (fully deterministic)."""
    config_kwargs.setdefault("binary", "dfget")
    return DfgetClient(DfgetConfig(**config_kwargs), env={})


def run_result(stdout: str = "", stderr: str = "", returncode: int = 0):
    return subprocess.CompletedProcess(
        args=["dfget"], returncode=returncode, stdout=stdout, stderr=stderr
    )


# ---------------------------------------------------------------------------
# argv construction
# ---------------------------------------------------------------------------


def test_argv_minimal() -> None:
    argv = make_client().build_download_argv(URL, OUTPUT)
    assert argv == [
        "dfget",
        URL,
        "-O",
        OUTPUT,
        "--timeout",
        "2h",
        "--console",
    ]


def test_argv_every_flag() -> None:
    client = DfgetClient(
        DfgetConfig(
            binary="dfget",
            endpoint="/var/run/dfdaemon.sock",
            log_level="warn",
        ),
        env={"DFDAEMON_ENDPOINT": "/ignored/when-explicit.sock"},
    )
    argv = client.build_download_argv(
        URL,
        OUTPUT,
        overwrite=True,
        force_hard_link=True,
        recursive=True,
        timeout="30m",
        digest="sha256:abc123",
        transfer_from_dfdaemon=True,
        content_for_calculating_task_id="cid-1",
        tag="my-tag",
        application="my-app",
        priority=3,
        background=True,
        extra_args=["--max-files", "10"],
        timeout_s=60.0,
    )
    assert argv == [
        "dfget",
        URL,
        "-O",
        OUTPUT,
        "-e",
        "/var/run/dfdaemon.sock",
        "--overwrite",
        "--force-hard-link",
        "--recursive",
        "--timeout",
        "30m",
        "--digest",
        "sha256:abc123",
        "--transfer-from-dfdaemon",
        "--content-for-calculating-task-id",
        "cid-1",
        "--tag",
        "my-tag",
        "--application",
        "my-app",
        "--priority",
        "3",
        "--background",
        "--log-level",
        "warn",
        "--console",
        "--max-files",
        "10",
    ]
    # timeout_s never shows up in argv - it only caps the subprocess wait
    assert "--timeout-s" not in argv
    assert "60.0" not in argv


def test_argv_boolean_flags_omitted_when_false() -> None:
    argv = make_client().build_download_argv(
        URL,
        OUTPUT,
        overwrite=False,
        force_hard_link=False,
        recursive=False,
        transfer_from_dfdaemon=False,
        background=False,
    )
    for flag in (
        "--overwrite",
        "--force-hard-link",
        "--recursive",
        "--transfer-from-dfdaemon",
        "--background",
    ):
        assert flag not in argv


def test_argv_none_values_omitted() -> None:
    argv = make_client().build_download_argv(
        URL,
        OUTPUT,
        digest=None,
        content_for_calculating_task_id=None,
        tag=None,
        application=None,
        priority=None,
    )
    for flag in (
        "--digest",
        "--content-for-calculating-task-id",
        "--tag",
        "--application",
        "--priority",
    ):
        assert flag not in argv


def test_argv_timeout_none_falls_back_to_config_default() -> None:
    client = DfgetClient(
        DfgetConfig(binary="dfget", default_timeout="45s"), env={}
    )
    argv = client.build_download_argv(URL, OUTPUT, timeout=None)
    assert argv == [
        "dfget",
        URL,
        "-O",
        OUTPUT,
        "--timeout",
        "45s",
        "--console",
    ]


def test_argv_endpoint_from_env_passthrough() -> None:
    client = DfgetClient(
        DfgetConfig(binary="dfget"),
        env={"DFDAEMON_ENDPOINT": "/var/run/dfdaemon.sock"},
    )
    argv = client.build_download_argv(URL, OUTPUT)
    assert argv[:4] == ["dfget", URL, "-O", OUTPUT]
    assert argv[4:6] == ["-e", "/var/run/dfdaemon.sock"]


def test_argv_explicit_endpoint_beats_env() -> None:
    client = DfgetClient(
        DfgetConfig(binary="dfget", endpoint="/explicit.sock"),
        env={"DFDAEMON_ENDPOINT": "/env.sock"},
    )
    argv = client.build_download_argv(URL, OUTPUT)
    assert "/explicit.sock" in argv
    assert "/env.sock" not in argv


def test_argv_missing_binary_raises(path_env) -> None:
    client = DfgetClient(DfgetConfig(), env=dict(path_env))
    with pytest.raises(DfgetNotFoundError):
        client.build_download_argv(URL, OUTPUT)


# ---------------------------------------------------------------------------
# download() - success parsing
# ---------------------------------------------------------------------------

TASK_ID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"


def test_download_success_parsing(monkeypatch) -> None:
    stdout = (
        "INFO downloading {0}\n".format(URL)
        + "INFO task id: {0}\n".format(TASK_ID)
        + "42% [####>-------] 12.3 MiB/s\n"
        + "INFO downloaded 2048 bytes\n"
        + "INFO download finished\n"
    )
    monkeypatch.setattr(
        "dragonfly_dfget.client.subprocess.run",
        lambda *a, **k: run_result(stdout=stdout, stderr="warn: something\n"),
    )
    client = make_client()
    result = client.download(URL, OUTPUT)
    assert isinstance(result, DownloadResult)
    assert result.output_path == os.path.abspath(OUTPUT)
    assert result.task_id == TASK_ID
    assert result.downloaded_size == 2048
    assert result.progress_lines == ("42% [####>-------] 12.3 MiB/s",)
    assert result.raw_stdout == stdout
    assert result.raw_stderr == "warn: something\n"
    assert result.returncode == 0
    assert result.duration_s >= 0.0


def test_download_passes_timeout_s_to_subprocess(monkeypatch) -> None:
    captured = {}

    def fake_run(argv, **kwargs):
        captured["argv"] = argv
        captured["kwargs"] = kwargs
        return run_result(stdout="")

    monkeypatch.setattr("dragonfly_dfget.client.subprocess.run", fake_run)
    client = make_client()
    client.download(URL, OUTPUT, timeout_s=5.0)
    assert captured["kwargs"]["timeout"] == 5.0
    assert captured["kwargs"]["capture_output"] is True
    assert captured["kwargs"]["text"] is True
    # argv itself unaffected by timeout_s
    assert captured["argv"] == [
        "dfget",
        URL,
        "-O",
        OUTPUT,
        "--timeout",
        "2h",
        "--console",
    ]


def test_download_without_task_id_is_fine(monkeypatch) -> None:
    monkeypatch.setattr(
        "dragonfly_dfget.client.subprocess.run",
        lambda *a, **k: run_result(stdout="done\n"),
    )
    result = make_client().download(URL, OUTPUT)
    assert result.task_id is None
    assert result.downloaded_size is None
    assert result.progress_lines == ()


# ---------------------------------------------------------------------------
# download() - error mapping
# ---------------------------------------------------------------------------


def test_download_nonzero_exit_raises_command_error(monkeypatch) -> None:
    monkeypatch.setattr(
        "dragonfly_dfget.client.subprocess.run",
        lambda *a, **k: run_result(
            stdout="some output", stderr="ERROR connecting dfdaemon", returncode=1
        ),
    )
    client = make_client()
    with pytest.raises(DfgetCommandError) as excinfo:
        client.download(URL, OUTPUT)
    err = excinfo.value
    assert err.returncode == 1
    assert err.stdout == "some output"
    assert err.stderr == "ERROR connecting dfdaemon"
    assert err.argv == ["dfget", URL, "-O", OUTPUT, "--timeout", "2h", "--console"]
    assert isinstance(err, DfgetError)


def test_download_file_not_found_raises_not_found(monkeypatch) -> None:
    def fake_run(*a, **k):
        raise FileNotFoundError("dfget")

    monkeypatch.setattr("dragonfly_dfget.client.subprocess.run", fake_run)
    client = make_client()
    with pytest.raises(DfgetNotFoundError):
        client.download(URL, OUTPUT)


def test_download_timeout_expired_raises_timeout_error(monkeypatch) -> None:
    def fake_run(argv, **kwargs):
        raise subprocess.TimeoutExpired(
            cmd=argv, timeout=kwargs.get("timeout"), output=b"", stderr=b""
        )

    monkeypatch.setattr("dragonfly_dfget.client.subprocess.run", fake_run)
    client = make_client()
    with pytest.raises(DfgetTimeoutError) as excinfo:
        client.download(URL, OUTPUT, timeout_s=1.0)
    assert excinfo.value.timeout_s == 1.0
    assert excinfo.value.argv == [
        "dfget",
        URL,
        "-O",
        OUTPUT,
        "--timeout",
        "2h",
        "--console",
    ]


# ---------------------------------------------------------------------------
# version() / health()
# ---------------------------------------------------------------------------


def test_version_argv_and_output(monkeypatch) -> None:
    captured = {}

    def fake_run(argv, **kwargs):
        captured["argv"] = argv
        return run_result(stdout="dfget 2.5.3\n")

    monkeypatch.setattr("dragonfly_dfget.client.subprocess.run", fake_run)
    assert make_client().version() == "dfget 2.5.3"
    assert captured["argv"] == ["dfget", "--version"]


def test_version_missing_binary(path_env) -> None:
    client = DfgetClient(DfgetConfig(), env=dict(path_env))
    with pytest.raises(DfgetNotFoundError):
        client.version()


def test_version_failure_maps_to_command_error(monkeypatch) -> None:
    monkeypatch.setattr(
        "dragonfly_dfget.client.subprocess.run",
        lambda *a, **k: run_result(stderr="boom", returncode=2),
    )
    with pytest.raises(DfgetCommandError) as excinfo:
        make_client().version()
    assert excinfo.value.returncode == 2


def test_health_ok(monkeypatch) -> None:
    monkeypatch.setattr(
        "dragonfly_dfget.client.subprocess.run",
        lambda *a, **k: run_result(stdout="dfget 2.5.3\n"),
    )
    health = make_client().health()
    assert isinstance(health, HealthResult)
    assert health.healthy is True
    assert health.binary == "dfget"
    assert health.version == "dfget 2.5.3"
    assert health.message == "ok"


def test_health_missing_binary_does_not_raise(path_env) -> None:
    health = DfgetClient(DfgetConfig(), env=dict(path_env)).health()
    assert health.healthy is False
    assert health.binary is None
    assert health.version is None
    assert "not found" in health.message


def test_health_version_failure(monkeypatch) -> None:
    monkeypatch.setattr(
        "dragonfly_dfget.client.subprocess.run",
        lambda *a, **k: run_result(stderr="bad", returncode=3),
    )
    health = make_client().health()
    assert health.healthy is False
    assert "exit code 3" in health.message


def test_health_as_dict(monkeypatch) -> None:
    monkeypatch.setattr(
        "dragonfly_dfget.client.subprocess.run",
        lambda *a, **k: run_result(stdout="dfget 2.5.3\n"),
    )
    payload = make_client().health().as_dict()
    assert payload == {
        "healthy": True,
        "binary": "dfget",
        "version": "dfget 2.5.3",
        "message": "ok",
    }


# ---------------------------------------------------------------------------
# adownload() - async twin
# ---------------------------------------------------------------------------


class FakeAsyncProc:
    def __init__(
        self,
        returncode: int = 0,
        stdout: bytes = b"",
        stderr: bytes = b"",
        delay: float = 0.0,
    ) -> None:
        self.returncode = returncode
        self._stdout = stdout
        self._stderr = stderr
        self._delay = delay
        self.killed = False

    async def communicate(self):
        if self._delay:
            await asyncio.sleep(self._delay)
        return self._stdout, self._stderr

    def kill(self) -> None:
        self.killed = True
        self.returncode = -9


@pytest.mark.asyncio
async def test_adownload_success(monkeypatch) -> None:
    captured = {}

    async def fake_exec(*argv, **kwargs):
        captured["argv"] = list(argv)
        captured["kwargs"] = kwargs
        return FakeAsyncProc(
            stdout=(
                "INFO task id: {0}\nINFO downloaded 10 bytes\n".format(TASK_ID).encode()
            )
        )

    monkeypatch.setattr(
        "dragonfly_dfget.client.asyncio.create_subprocess_exec", fake_exec
    )
    result = await make_client().adownload(URL, OUTPUT, priority=1)
    assert captured["argv"] == [
        "dfget",
        URL,
        "-O",
        OUTPUT,
        "--timeout",
        "2h",
        "--priority",
        "1",
        "--console",
    ]
    assert isinstance(result, DownloadResult)
    assert result.task_id == TASK_ID
    assert result.downloaded_size == 10
    assert result.duration_s >= 0.0


@pytest.mark.asyncio
async def test_adownload_nonzero_exit(monkeypatch) -> None:
    async def fake_exec(*argv, **kwargs):
        return FakeAsyncProc(returncode=1, stderr=b"ERROR dfdaemon down")

    monkeypatch.setattr(
        "dragonfly_dfget.client.asyncio.create_subprocess_exec", fake_exec
    )
    with pytest.raises(DfgetCommandError) as excinfo:
        await make_client().adownload(URL, OUTPUT)
    assert excinfo.value.returncode == 1
    assert excinfo.value.stderr == "ERROR dfdaemon down"


@pytest.mark.asyncio
async def test_adownload_missing_binary(path_env) -> None:
    client = DfgetClient(DfgetConfig(), env=dict(path_env))
    with pytest.raises(DfgetNotFoundError):
        await client.adownload(URL, OUTPUT)


@pytest.mark.asyncio
async def test_adownload_timeout(monkeypatch) -> None:
    async def fake_exec(*argv, **kwargs):
        return FakeAsyncProc(delay=30.0)

    monkeypatch.setattr(
        "dragonfly_dfget.client.asyncio.create_subprocess_exec", fake_exec
    )
    with pytest.raises(DfgetTimeoutError) as excinfo:
        await make_client().adownload(URL, OUTPUT, timeout_s=0.01)
    assert excinfo.value.timeout_s == 0.01


@pytest.mark.asyncio
async def test_adownload_without_timeout_s_skips_wait_for(monkeypatch) -> None:
    async def fake_exec(*argv, **kwargs):
        return FakeAsyncProc(stdout=b"ok\n", delay=0.0)

    monkeypatch.setattr(
        "dragonfly_dfget.client.asyncio.create_subprocess_exec", fake_exec
    )
    result = await make_client().adownload(URL, OUTPUT)
    assert result.raw_stdout == "ok\n"


def test_version_module_exports() -> None:
    assert __version__ == "0.1.0"
