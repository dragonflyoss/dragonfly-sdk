"""Tests for synchronous DfcacheClient methods.

Everything goes through the ``mock_run`` fixture, which monkeypatches
:func:`subprocess.run`, so no real ``dfcache`` binary or cluster is
involved.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from dragonfly_dfcache import (
    DfcacheClient,
    DfcacheCommandError,
    DfcacheConfig,
    DfcacheNotFoundError,
    DfcacheTimeoutError,
)


# ----------------------------------------------------------------------
# Command construction helpers
# ----------------------------------------------------------------------
def _flat(argv) -> list:
    return [str(part) for part in argv]


def _argv(client, subcommand, *args):
    """Expected full argv for a dfcache subcommand."""
    return [client.config.binary, subcommand, *args]


class TestStatArgv:
    def test_stat_builds_expected_argv(self, client, mock_run):
        mock_run.result = (0, "task_id = t1\nsize = 1024", "")
        result = client.stat("t1")

        assert mock_run.commands == [
            _argv(client, "stat", "t1", "--endpoint", client.config.endpoint)
        ]
        assert result == {"task_id": "t1", "size": 1024}

    def test_stat_omits_endpoint_when_unset(self, mock_run):
        client = DfcacheClient(DfcacheConfig(binary_path="dfcache", endpoint=""))
        client.stat("t1")
        assert mock_run.commands == [
            _argv(client, "stat", "t1")
        ]

    def test_stat_emits_tls_flags_when_enabled(self, mock_run):
        client = DfcacheClient(
            DfcacheConfig(
                binary_path="dfcache",
                endpoint="",
                tls=True,
                tls_ca_cert="/etc/ca.pem",
            )
        )
        client.stat("t1")
        assert mock_run.commands == [
            _argv(client, "stat", "t1", "--tls", "--tls-ca-cert", "/etc/ca.pem")
        ]


class TestImportArgv:
    def test_import_defaults(self, client, mock_run):
        client.import_file("/data/big.bin")

        argv = mock_run.commands[0]
        assert argv[:3] == [client.config.binary, "import", "/data/big.bin"]
        assert "--persistent-replica-count=1" in argv
        assert mock_run.commands == [
            _argv(client, "import", "/data/big.bin", "--persistent-replica-count=1",
                  "--endpoint", client.config.endpoint)
        ]

    def test_import_flag_handling(self, client, mock_run):
        client.import_file(
            "/data/big.bin",
            persistent_replica_count=3,
            content_for_calculating_task_id="abc123",
        )
        argv = mock_run.commands[0]
        assert "--persistent-replica-count=3" in argv
        assert "--content-for-calculating-task-id=abc123" in argv

    def test_import_omits_content_flag_when_unspecified(self, client, mock_run):
        client.import_file("/data/big.bin", persistent_replica_count=2)
        argv = mock_run.commands[0]
        assert not any(arg.startswith("--content-for") for arg in argv)


class TestExportArgv:
    def test_export_defaults(self, client, mock_run):
        client.export("t1", "/tmp/out.bin")

        assert mock_run.commands == [
            _argv(client, "export", "t1", "--output=/tmp/out.bin",
                  "--endpoint", client.config.endpoint)
        ]

    def test_export_transfer_flag(self, client, mock_run):
        client.export("t1", "/tmp/out.bin", transfer_from_dfdaemon=True)
        argv = mock_run.commands[0]
        assert "--output=/tmp/out.bin" in argv
        assert "--transfer-from-dfdaemon" in argv

    def test_export_transfer_flag_omitted_by_default(self, client, mock_run):
        client.export("t1", "/tmp/out.bin")
        assert "--transfer-from-dfdaemon" not in _flat(mock_run.commands[0])


class TestDeleteArgv:
    def test_delete_builds_expected_argv(self, client, mock_run):
        client.delete("t1")
        assert mock_run.commands == [
            _argv(client, "delete", "t1", "--endpoint", client.config.endpoint)
        ]


class TestVersionArgv:
    def test_version_builds_expected_argv(self, client, mock_run):
        mock_run.result = (0, "dfcache v1.2.3", "")
        result = client.version()
        assert mock_run.commands == [
            _argv(client, "version", "--endpoint", client.config.endpoint)
        ]
        # The regex captures the bare version number.
        assert result["version"] == "1.2.3"

    def test_version_extracts_from_human_output(self, client, mock_run):
        mock_run.result = (0, "", "release: 2.0.0")
        result = client.version()
        assert result["version"] == "2.0.0"


class TestConfigOverrides:
    def test_endpoint_flag_uses_config_value(self, mock_run):
        client = DfcacheClient(
            DfcacheConfig(binary_path="dfcache", endpoint="http://manager:65001")
        )
        client.stat("t1")
        assert "--endpoint" in _flat(mock_run.commands[0])
        assert "http://manager:65001" in _flat(mock_run.commands[0])

    def test_binary_override_appears_in_argv(self, tmp_path, mock_run):
        fake_binary = tmp_path / "dfcache-fake.exe"
        fake_binary.write_text("", encoding="utf-8")
        client = DfcacheClient(DfcacheConfig(binary_path=str(fake_binary)))
        client.stat("t1")
        assert mock_run.commands[0][0] == str(fake_binary)


class TestTimeoutPassthrough:
    def test_config_timeout_used_by_default(self, mock_run):
        client = DfcacheClient(
            DfcacheConfig(binary_path="dfcache", endpoint="", timeout=1.5)
        )
        client.stat("t1")
        assert mock_run.timeouts == [1.5]

    def test_per_call_timeout_overrides_config(self, mock_run):
        client = DfcacheClient(
            DfcacheConfig(binary_path="dfcache", endpoint="", timeout=30.0)
        )
        client.stat("t1", timeout=2.25)
        assert mock_run.timeouts == [2.25]

    def test_explicit_none_falls_back_to_config(self, mock_run):
        client = DfcacheClient(
            DfcacheConfig(binary_path="dfcache", endpoint="", timeout=7.0)
        )
        client.stat("t1", timeout=None)
        assert mock_run.timeouts == [7.0]


class TestHealth:
    def test_health_true_on_success(self, client, mock_run):
        mock_run.result = (0, "version = 1.0.0", "")
        assert client.health() is True

    @pytest.mark.parametrize(
        "error",
        [
            FileNotFoundError("dfcache"),
            DfcacheNotFoundError("dfcache"),
            DfcacheTimeoutError("boom"),
            DfcacheCommandError(exit_code=1, stdout="", stderr="boom"),
        ],
        ids=["file-not-found", "not-found", "timeout", "command"],
    )
    def test_health_false_on_error(self, mock_run, client, error):
        mock_run.error = error
        assert client.health() is False

    def test_health_false_on_nonzero_exit(self, mock_run, client):
        mock_run.result = (1, "", "boom")
        assert client.health() is False
