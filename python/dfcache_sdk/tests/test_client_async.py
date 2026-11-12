"""Tests for asyncio-native DfcacheClient methods (astat/aimport/...).

Async subprocess calls are mocked via the ``mock_asyncproc`` fixture,
which monkeypatches :func:`asyncio.create_subprocess_exec`, so no real
``dfcache`` binary, cluster or network is involved.
"""

from __future__ import annotations

import json

import pytest

from dragonfly_dfcache import (
    DfcacheClient,
    DfcacheCommandError,
    DfcacheConfig,
    DfcacheNotFoundError,
    DfcacheTimeoutError,
)


def _argv(client, subcommand, *args):
    """Expected full argv for an async dfcache subcommand."""
    return [client.config.binary, subcommand, *args]


class TestAsyncArgvAndResults:
    @pytest.mark.asyncio
    async def test_astat(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, '{"size": 4096}', "")
        result = await client.astat("t1")

        assert mock_asyncproc.commands == [
            _argv(client, "stat", "t1", "--endpoint", client.config.endpoint)
        ]
        assert result == {"size": 4096}

    @pytest.mark.asyncio
    async def test_aimport_defaults_and_flags(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, "task_id = imported-1", "")

        result = await client.aimport("/data/big.bin")
        assert mock_asyncproc.commands[-1] == _argv(
            client, "import", "/data/big.bin", "--persistent-replica-count=1",
            "--endpoint", client.config.endpoint,
        )
        assert result == {"task_id": "imported-1"}

        await client.aimport(
            "/data/big.bin",
            persistent_replica_count=2,
            content_for_calculating_task_id="sha-abc",
        )
        argv = mock_asyncproc.commands[-1]
        assert "--persistent-replica-count=2" in argv
        assert "--content-for-calculating-task-id=sha-abc" in argv

    @pytest.mark.asyncio
    async def test_aexport(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, "output = /tmp/out.bin", "")
        result = await client.aexport("t1", "/tmp/out.bin", transfer_from_dfdaemon=True)

        assert mock_asyncproc.commands == [
            _argv(client, "export", "t1", "--output=/tmp/out.bin",
                  "--transfer-from-dfdaemon", "--endpoint", client.config.endpoint)
        ]
        assert result == {"output": "/tmp/out.bin"}

    @pytest.mark.asyncio
    async def test_adelete(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, "task_id = t1\nstatus = deleted", "")
        result = await client.adelete("t1")

        assert mock_asyncproc.commands == [
            _argv(client, "delete", "t1", "--endpoint", client.config.endpoint)
        ]
        assert result == {"task_id": "t1", "status": "deleted"}

    @pytest.mark.asyncio
    async def test_aversion_json(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, json.dumps({"version": "v1.2.3"}), "")
        result = await client.aversion()

        assert mock_asyncproc.commands == [
            _argv(client, "version", "--endpoint", client.config.endpoint)
        ]
        assert result == {"version": "v1.2.3"}

    @pytest.mark.asyncio
    async def test_aversion_extract_from_human_output(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, "", "dfcache version 3.4.5")
        result = await client.aversion()
        assert result["version"] == "3.4.5"


class TestAsyncErrorMapping:
    @pytest.mark.asyncio
    async def test_nonzero_exit_raises_command_error(self, client, mock_asyncproc):
        mock_asyncproc.result = (1, "out", "boom")
        with pytest.raises(DfcacheCommandError) as excinfo:
            await client.astat("t1")
        assert excinfo.value.exit_code == 1
        assert excinfo.value.stdout == "out"
        assert excinfo.value.stderr == "boom"

    @pytest.mark.asyncio
    async def test_exit_127_maps_to_not_found(self, client, mock_asyncproc):
        mock_asyncproc.result = (127, "", "")
        with pytest.raises(DfcacheNotFoundError):
            await client.astat("t1")

    @pytest.mark.asyncio
    async def test_file_not_found_raises_not_found(self, client, mock_asyncproc):
        mock_asyncproc.error = FileNotFoundError(2, "No such file")
        with pytest.raises(DfcacheNotFoundError):
            await client.astat("t1")

    @pytest.mark.asyncio
    async def test_timeout_raises_timeout_error(self, client, mock_asyncproc):
        # The fake communicate() blocks far longer than the call timeout,
        # so asyncio.wait_for expires and DfcacheTimeoutError is raised.
        mock_asyncproc.result = (0, '{"size": 1}', "")
        mock_asyncproc.delay = 10.0
        with pytest.raises(DfcacheTimeoutError):
            await client.astat("t1", timeout=0.05)

    @pytest.mark.asyncio
    async def test_delay_within_timeout_returns_normally(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, "task_id = t1", "")
        mock_asyncproc.delay = 0.01
        result = await client.astat("t1", timeout=1.0)
        assert result == {"task_id": "t1"}


class TestAsyncHealth:
    @pytest.mark.asyncio
    async def test_ahealth_true(self, client, mock_asyncproc):
        mock_asyncproc.result = (0, "version = 1.0.0", "")
        assert await client.ahealth() is True

    @pytest.mark.asyncio
    async def test_ahealth_false_on_command_error(self, client, mock_asyncproc):
        mock_asyncproc.result = (1, "", "boom")
        assert await client.ahealth() is False

    @pytest.mark.asyncio
    async def test_ahealth_false_on_timeout(self, mock_asyncproc):
        client = DfcacheClient(
            DfcacheConfig(binary_path="dfcache", endpoint="", timeout=0.03)
        )
        # Delay the fake communicate() longer than the configured timeout.
        mock_asyncproc.result = (0, "", "")
        mock_asyncproc.delay = 10.0
        assert await client.ahealth(timeout=0.05) is False
