"""Tests for the ``dfcache-sdk`` CLI entry point (dragonfly_dfcache.cli).

All dfcache invocations are mocked through the ``mock_run`` fixture.
"""

from __future__ import annotations

import json
import subprocess

import pytest

from dragonfly_dfcache import cli


@pytest.fixture
def run_cli(mock_run):
    """Invoke :func:`cli.main` with a fixed config, returning (exit_code, argvs)."""

    def _run(argv):
        code = cli.main(argv)
        return code, mock_run.commands

    return _run


class TestCliSmoke:
    def test_version_emits_json(self, run_cli, capsys):
        code, _ = run_cli(["version"])
        assert code == 0
        payload = json.loads(capsys.readouterr().out)
        assert isinstance(payload, dict)

    def test_stat_success_and_flag_passthrough(self, run_cli, capsys):
        capsys.readouterr()
        code, argvs = run_cli(["--endpoint", "http://mgr:1", "stat", "t1"])
        assert code == 0
        json.loads(capsys.readouterr().out)  # stdout is valid JSON
        assert "http://mgr:1" in argvs[0]

    def test_stat_output_is_json(self, run_cli, capsys):
        capsys.readouterr()
        code, _ = run_cli(["stat", "t1"])
        assert code == 0
        assert json.loads(capsys.readouterr().out) == {}

    def test_import_and_delete_succeed(self, run_cli, capsys):
        capsys.readouterr()
        assert run_cli(["import", "/data/f.bin"])[0] == 0
        assert run_cli(["delete", "t1"])[0] == 0

    def test_export_requires_output(self, capsys):
        assert cli.main(["export", "t1"]) == 2
        assert "requires --output" in capsys.readouterr().err

    def test_export_with_output_passes_flag(self, run_cli):
        code, argvs = run_cli(["export", "t1", "--output", "/tmp/out.bin"])
        assert code == 0
        assert "--output=/tmp/out.bin" in argvs[0]

    def test_binary_failure_maps_to_exit_1(self, monkeypatch, capsys):
        def raise_fnf(*args, **kwargs):
            raise FileNotFoundError(2, "dfcache")

        monkeypatch.setattr(subprocess, "run", raise_fnf)
        assert cli.main(["stat", "t1"]) == 1
        assert "error:" in capsys.readouterr().err

    def test_timeout_maps_to_exit_1(self, monkeypatch, capsys):
        def raise_timeout(*args, **kwargs):
            raise subprocess.TimeoutExpired(cmd="dfcache", timeout=1.0)

        monkeypatch.setattr(subprocess, "run", raise_timeout)
        assert cli.main(["stat", "t1"]) == 1

    def test_health_healthy(self, run_cli, capsys):
        code, _ = run_cli(["--endpoint", "http://mgr:1", "health"])
        assert code == 0
        assert capsys.readouterr().out.strip() == "healthy"

    def test_health_unhealthy_exits_1(self, run_cli, capsys, monkeypatch):
        capsys.readouterr()
        # Health's underlying version probe reports a fatal CLI error.
        monkeypatch.setattr(
            cli.DfcacheClient, "health", lambda self, timeout=None: False
        )
        code, _ = run_cli(["health"])
        assert code == 1
        assert capsys.readouterr().out.strip() == "unhealthy"
