"""Tests for DfcacheConfig: env resolution and validation.

Uses ``clean_env`` to isolate ``DFCACHE_*`` variables so results are
deterministic on any host.
"""

from __future__ import annotations

import pytest

from dragonfly_dfcache import DfcacheConfig
from dragonfly_dfcache.config import (
    DEFAULT_ENDPOINT,
    DEFAULT_TIMEOUT,
    resolve_timeout,
)


class TestDefaults:
    def test_default_endpoint_and_timeout(self, clean_env):
        config = DfcacheConfig(binary_path="dfcache")
        assert config.endpoint == DEFAULT_ENDPOINT
        assert config.timeout == DEFAULT_TIMEOUT
        assert config.tls is False
        assert config.tls_ca_cert is None

    def test_explicit_values_win_over_defaults(self, clean_env):
        config = DfcacheConfig(
            endpoint="http://manager:65001",
            timeout=5.0,
            binary_path="/usr/local/bin/dfcache",
        )
        assert config.endpoint == "http://manager:65001"
        assert config.timeout == 5.0
        assert config.binary_path == "/usr/local/bin/dfcache"

    def test_binary_property_returns_binary_path(self, clean_env):
        config = DfcacheConfig(binary_path="/usr/local/bin/dfcache")
        assert config.binary == "/usr/local/bin/dfcache"

    def test_negative_timeout_rejected(self, clean_env):
        with pytest.raises(ValueError):
            DfcacheConfig(binary_path="dfcache", timeout=-1)


class TestEnvOverrides:
    def test_endpoint_env(self, monkeypatch, clean_env):
        monkeypatch.setenv("DFCACHE_ENDPOINT", "http://env-manager:65001")
        config = DfcacheConfig(binary_path="dfcache")
        assert config.endpoint == "http://env-manager:65001"

    def test_timeout_env(self, monkeypatch, clean_env):
        monkeypatch.setenv("DFCACHE_TIMEOUT", "12.5")
        assert resolve_timeout() == 12.5

    def test_invalid_timeout_env_raises(self, monkeypatch, clean_env):
        monkeypatch.setenv("DFCACHE_TIMEOUT", "not-a-number")
        with pytest.raises(ValueError):
            resolve_timeout()

    def test_zero_timeout_env_raises(self, monkeypatch, clean_env):
        monkeypatch.setenv("DFCACHE_TIMEOUT", "0")
        with pytest.raises(ValueError):
            resolve_timeout()

    def test_tls_env_truthy_values(self, monkeypatch, clean_env):
        for raw in ("1", "true", "yes", "on", "TRUE"):
            monkeypatch.setenv("DFCACHE_TLS", raw)
            assert DfcacheConfig(binary_path="dfcache").tls is True, raw

    def test_tls_env_falsy_values(self, monkeypatch, clean_env):
        for raw in ("0", "false", "no", "off"):
            monkeypatch.setenv("DFCACHE_TLS", raw)
            assert DfcacheConfig(binary_path="dfcache").tls is False, raw

    def test_tls_ca_cert_env(self, monkeypatch, clean_env):
        monkeypatch.setenv("DFCACHE_TLS_CA_CERT", "/etc/ca.pem")
        config = DfcacheConfig(binary_path="dfcache")
        assert config.tls_ca_cert == "/etc/ca.pem"


class TestExplicitOverridesEnv:
    def test_explicit_beats_env(self, monkeypatch, clean_env):
        monkeypatch.setenv("DFCACHE_ENDPOINT", "http://from-env:1")
        monkeypatch.setenv("DFCACHE_TIMEOUT", "9")
        monkeypatch.setenv("DFCACHE_TLS_CA_CERT", "/env-ca.pem")
        config = DfcacheConfig(
            endpoint="http://explicit:2",
            timeout=3.0,
            tls_ca_cert="/explicit-ca.pem",
            binary_path="dfcache",
        )
        assert config.endpoint == "http://explicit:2"
        assert config.timeout == 3.0
        assert config.tls_ca_cert == "/explicit-ca.pem"

    def test_binary_env(self, monkeypatch, clean_env):
        monkeypatch.setenv("DFCACHE_BINARY", "/custom/dfcache")
        config = DfcacheConfig(binary_path=None)


class TestBinaryResolution:
    def test_missing_absolute_binary_path_raises_not_found(self, tmp_path, mock_run):
        from dragonfly_dfcache import DfcacheClient, DfcacheNotFoundError

        client = DfcacheClient(DfcacheConfig(binary_path=str(tmp_path / "absent")))
        with pytest.raises(DfcacheNotFoundError):
            client.stat("t1")
        # The subprocess was never attempted.
        assert mock_run.commands == []

    def test_bare_binary_name_skips_early_check(self, mock_run, clean_env):
        from dragonfly_dfcache import DfcacheClient

        client = DfcacheClient(DfcacheConfig(binary_path="dfcache"))
        client.stat("t1")
        # PATH resolution is left to subprocess; the command still ran (mocked).
        assert mock_run.commands == [[client.config.binary, "stat", "t1",
                                      "--endpoint", client.config.endpoint]]
