"""Output parsing, error hierarchy, and CLI smoke tests (argparse + --json)."""

from __future__ import annotations

import json

import pytest

import dragonfly_dfget.cli as cli_mod
from dragonfly_dfget import DfgetClient, DownloadResult, HealthResult
from dragonfly_dfget.cli import build_parser, main
from dragonfly_dfget.client import (
    parse_downloaded_size,
    parse_progress_lines,
    parse_task_id,
)
from dragonfly_dfget.errors import (
    DfgetCommandError,
    DfgetError,
    DfgetNotFoundError,
    DfgetTimeoutError,
)

TASK_ID = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
UUID = "123e4567-e89b-12d3-a456-426614174000"


# ---------------------------------------------------------------------------
# parse_task_id
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "text,expected",
    [
        ("INFO task id: " + TASK_ID, TASK_ID),
        ("task_id=" + TASK_ID, TASK_ID),
        ("TaskID: " + TASK_ID, TASK_ID),
        ("task-id '" + TASK_ID + "'", TASK_ID),
        ("download task id " + UUID, UUID),
        ("nothing interesting here", None),
        ("task id: ", None),
        ("", None),
    ],
)
def test_parse_task_id(text: str, expected) -> None:
    assert parse_task_id(text) == expected


def test_parse_task_id_prefers_first_match() -> None:
    text = "task id: {0}\nlater task id: {1}".format(TASK_ID, UUID)
    assert parse_task_id(text) == TASK_ID


# ---------------------------------------------------------------------------
# parse_downloaded_size / parse_progress_lines
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "text,expected",
    [
        ("INFO downloaded 2048 bytes", 2048),
        ("Downloaded: 1,024 bytes", 1024),
        ("downloaded 1.5 MiB", 1572864),
        ("downloaded size = 10 KB", 10240),
        ("total size: 512", 512),
        ("file size=4096 bytes", 4096),
        ("no numbers with units here", None),
        ("", None),
    ],
)
def test_parse_downloaded_size(text: str, expected) -> None:
    assert parse_downloaded_size(text) == expected


def test_parse_progress_lines() -> None:
    text = "\n".join(
        [
            "INFO start",
            "42% [####>-------] 12.3 MiB/s",
            "100% [==========] done",
            "INFO finished",
        ]
    )
    assert parse_progress_lines(text) == (
        "42% [####>-------] 12.3 MiB/s",
        "100% [==========] done",
    )
    assert parse_progress_lines("") == ()


# ---------------------------------------------------------------------------
# error hierarchy
# ---------------------------------------------------------------------------


def test_error_hierarchy() -> None:
    assert issubclass(DfgetError, Exception)
    assert issubclass(DfgetNotFoundError, DfgetError)
    assert issubclass(DfgetTimeoutError, DfgetError)
    assert issubclass(DfgetCommandError, DfgetError)


def test_not_found_default_message() -> None:
    err = DfgetNotFoundError()
    assert "dfget binary not found" in str(err)
    assert err.binary is None
    err2 = DfgetNotFoundError(binary="/usr/local/bin/dfget")
    assert err2.binary == "/usr/local/bin/dfget"


def test_command_error_attributes_and_str() -> None:
    err = DfgetCommandError(
        "dfget download failed",
        returncode=7,
        stdout="out",
        stderr="ERROR: dfdaemon unavailable",
        argv=["dfget", "u", "-O", "o"],
    )
    assert err.returncode == 7
    assert err.stdout == "out"
    assert err.stderr == "ERROR: dfdaemon unavailable"
    assert err.argv == ["dfget", "u", "-O", "o"]
    assert "exit code 7" in str(err)
    assert "dfdaemon unavailable" in str(err)


def test_timeout_error_attributes() -> None:
    err = DfgetTimeoutError("dfget timed out after 1.0s", timeout_s=1.0, argv=["dfget"])
    assert err.timeout_s == 1.0
    assert err.argv == ["dfget"]


# ---------------------------------------------------------------------------
# CLI: argparse wiring
# ---------------------------------------------------------------------------


def test_parser_download_wiring() -> None:
    args = build_parser().parse_args(
        [
            "download",
            "https://example.com/f",
            "-O",
            "/tmp/f",
            "--json",
            "--overwrite",
            "--force-hard-link",
            "-r",
            "--timeout",
            "30m",
            "--timeout-s",
            "5",
            "--digest",
            "sha256:aa",
            "--transfer-from-dfdaemon",
            "--content-for-calculating-task-id",
            "cid",
            "--tag",
            "t",
            "--application",
            "a",
            "--priority",
            "2",
            "--background",
            "--extra-arg=--max-files",
            "--extra-arg=10",
            "--dfget-bin",
            "/opt/bin/dfget",
            "--endpoint",
            "/var/run/dfdaemon.sock",
        ]
    )
    assert args.command == "download"
    assert args.url == "https://example.com/f"
    assert args.output == "/tmp/f"
    assert args.json_output is True
    assert args.overwrite is True
    assert args.force_hard_link is True
    assert args.recursive is True
    assert args.timeout == "30m"
    assert args.timeout_s == 5.0
    assert args.digest == "sha256:aa"
    assert args.transfer_from_dfdaemon is True
    assert args.content_for_calculating_task_id == "cid"
    assert args.tag == "t"
    assert args.application == "a"
    assert args.priority == 2
    assert args.background is True
    assert args.extra_args == ["--max-files", "10"]
    assert args.dfget_bin == "/opt/bin/dfget"
    assert args.endpoint == "/var/run/dfdaemon.sock"


def test_parser_version_and_health_wiring() -> None:
    args = build_parser().parse_args(["version", "--json"])
    assert args.command == "version"
    assert args.json_output is True

    args = build_parser().parse_args(["health"])
    assert args.command == "health"
    assert args.json_output is False


def test_parser_requires_subcommand() -> None:
    with pytest.raises(SystemExit):
        build_parser().parse_args([])


def test_parser_download_requires_output() -> None:
    with pytest.raises(SystemExit):
        build_parser().parse_args(["download", "https://example.com/f"])


# ---------------------------------------------------------------------------
# CLI: main() behaviour with mocked client
# ---------------------------------------------------------------------------


def fake_download(result: DownloadResult):
    def _download(self, url, output, **kwargs):
        return result

    return _download


def test_cli_download_json_success(monkeypatch, capsys) -> None:
    result = DownloadResult(
        output_path="/tmp/f",
        task_id=TASK_ID,
        raw_stdout="",
        raw_stderr="",
        duration_s=1.25,
        downloaded_size=42,
    )
    monkeypatch.setattr(DfgetClient, "download", fake_download(result))
    code = main(["download", "https://example.com/f", "-O", "/tmp/f", "--json"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload == {
        "ok": True,
        "output_path": "/tmp/f",
        "task_id": TASK_ID,
        "downloaded_size": 42,
        "duration_s": 1.25,
        "returncode": 0,
    }


def test_cli_download_human_success(monkeypatch, capsys) -> None:
    result = DownloadResult(
        output_path="/tmp/f",
        task_id=None,
        raw_stdout="",
        raw_stderr="",
        duration_s=0.5,
    )
    monkeypatch.setattr(DfgetClient, "download", fake_download(result))
    code = main(["download", "https://example.com/f", "-O", "/tmp/f"])
    assert code == 0
    out = capsys.readouterr().out
    assert "downloaded: /tmp/f" in out
    assert "duration:  0.50s" in out


def test_cli_download_error_json(monkeypatch, capsys) -> None:
    def _boom(self, url, output, **kwargs):
        raise DfgetCommandError(
            "dfget download failed",
            returncode=1,
            stdout="",
            stderr="ERROR: boom",
            argv=["dfget"],
        )

    monkeypatch.setattr(DfgetClient, "download", _boom)
    code = main(["download", "https://example.com/f", "-O", "/tmp/f", "--json"])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert payload["ok"] is False
    assert payload["error"]["type"] == "DfgetCommandError"
    assert payload["error"]["returncode"] == 1
    assert payload["error"]["stderr"] == "ERROR: boom"


def test_cli_download_error_human_goes_to_stderr(monkeypatch, capsys) -> None:
    def _missing(self, url, output, **kwargs):
        raise DfgetNotFoundError()

    monkeypatch.setattr(DfgetClient, "download", _missing)
    code = main(["download", "https://example.com/f", "-O", "/tmp/f"])
    assert code == 1
    captured = capsys.readouterr()
    assert captured.out == ""
    assert "dfget binary not found" in captured.err


def test_cli_version_json(monkeypatch, capsys) -> None:
    monkeypatch.setattr(DfgetClient, "version", lambda self: "dfget 2.5.3")
    code = main(["version", "--json"])
    assert code == 0
    assert json.loads(capsys.readouterr().out) == {
        "ok": True,
        "version": "dfget 2.5.3",
    }


def test_cli_version_human(monkeypatch, capsys) -> None:
    monkeypatch.setattr(DfgetClient, "version", lambda self: "dfget 2.5.3")
    assert main(["version"]) == 0
    assert capsys.readouterr().out.strip() == "dfget 2.5.3"


def test_cli_health_json_healthy(monkeypatch, capsys) -> None:
    monkeypatch.setattr(
        DfgetClient,
        "health",
        lambda self: HealthResult(
            healthy=True, binary="dfget", version="dfget 2.5.3", message="ok"
        ),
    )
    code = main(["health", "--json"])
    assert code == 0
    payload = json.loads(capsys.readouterr().out)
    assert payload == {
        "ok": True,
        "healthy": True,
        "binary": "dfget",
        "version": "dfget 2.5.3",
        "message": "ok",
    }


def test_cli_health_json_unhealthy(monkeypatch, capsys) -> None:
    monkeypatch.setattr(
        DfgetClient,
        "health",
        lambda self: HealthResult(
            healthy=False, binary=None, version=None, message="dfget binary not found"
        ),
    )
    code = main(["health", "--json"])
    assert code == 1
    payload = json.loads(capsys.readouterr().out)
    assert payload["ok"] is False
    assert payload["healthy"] is False


def test_cli_module_exports() -> None:
    assert cli_mod.__name__ == "dragonfly_dfget.cli"
