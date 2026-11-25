"""Tests for output parsing (_OutputParser) and error mapping."""

from __future__ import annotations

import json
import subprocess

import pytest

from dragonfly_dfcache import (
    DfcacheClient,
    DfcacheCommandError,
    DfcacheConfig,
    DfcacheNotFoundError,
    DfcacheTimeoutError,
)
from dragonfly_dfcache.client import _OutputParser, _decode


# ----------------------------------------------------------------------
# _decode
# ----------------------------------------------------------------------
class TestDecode:
    def test_bytes_decoded_utf8(self):
        assert _decode(b'{"a": 1}') == '{"a": 1}'

    def test_bad_bytes_replaced(self):
        assert _decode(b"\xff\xfe") == "\ufffd\ufffd"

    def test_none_becomes_empty(self):
        assert _decode(None) == ""

    def test_str_passthrough(self):
        assert _decode("abc") == "abc"


# ----------------------------------------------------------------------
# _key_values / _coerce
# ----------------------------------------------------------------------
class TestKeyValues:
    def test_basic_pairs(self):
        text = "task_id = t1\nsize = 1024\n"
        assert _OutputParser._key_values(text) == {"task_id": "t1", "size": 1024}

    def test_quoted_values_stripped(self):
        text = 'key = "hello, world"\nother = \'single\''
        result = _OutputParser._key_values(text)
        assert result == {"key": "hello, world", "other": "single"}

    def test_comments_and_blank_lines_skipped(self):
        text = "# a comment\n\nreal = yes\n"
        assert _OutputParser._key_values(text) == {"real": "yes"}

    def test_lines_without_equals_skipped(self):
        text = "progress bar noise\ncount = 3\n"
        assert _OutputParser._key_values(text) == {"count": 3}

    def test_empty_key_skipped(self):
        text = "= novalue\ngood = 1"
        result = _OutputParser._key_values(text)
        assert result == {"good": 1}

    def test_empty_text_returns_empty_dict(self):
        assert _OutputParser._key_values("") == {}
        assert _OutputParser._key_values(None) == {}

    def test_multiple_equals_splits_on_first(self):
        text = "cmd = a=b"
        assert _OutputParser._key_values(text) == {"cmd": "a=b"}


class TestCoerce:
    @pytest.mark.parametrize(
        ("raw", "expected"),
        [
            ("true", True),
            ("True", True),
            ("false", False),
            ("null", None),
            ("none", None),
            ("42", 42),
            ("-7", -7),
            ("3.14", 3.14),
            ("-0.5", -0.5),
            ("hello", "hello"),
            ("42abc", "42abc"),
            ("", ""),
            ("+5", "+5"),  # deliberately not coerced
        ],
        ids=["true", "True", "false", "null", "none", "int", "neg-int",
             "float", "neg-float", "string", "alnum", "empty", "plus-int"],
    )
    def test_coercion(self, raw, expected):
        assert _OutputParser._coerce(raw) == expected

    def test_exponent_form_not_coerced(self):
        # Strict decimal float regex requires a fraction part.
        assert _OutputParser._coerce("1e3") == "1e3"


# ----------------------------------------------------------------------
# _OutputParser.parse
# ----------------------------------------------------------------------
class TestParse:
    def test_json_preferred(self):
        payload = {"task_id": "t1", "size": 2048}
        assert _OutputParser.parse(json.dumps(payload), "") == payload

    def test_json_with_progress_noise_before_it_not_parsed(self):
        # blob must *start* with "{" to be treated as JSON.
        stdout = "loading...\n{bad json"
        assert _OutputParser.parse(stdout, "") == {}

    def test_key_values_fallback(self):
        result = _OutputParser.parse("", "task_id = t1\nstatus = ok")
        assert result == {"task_id": "t1", "status": "ok"}

    def test_json_tried_before_key_values_per_source(self):
        stdout = "task_id = t1\n"
        stderr = '{"error": "boom"}'
        # stdout is consulted first; it yields key=value dict.
        result = _OutputParser.parse(stdout, stderr)
        assert result == {"task_id": "t1"}

    def test_both_empty_returns_empty_dict(self):
        assert _OutputParser.parse("", "") == {}

    def test_invalid_json_falls_back_to_key_values(self):
        stdout = '{"task_id": broken}\n'
        assert _OutputParser.parse(stdout, "") == {}


# ----------------------------------------------------------------------
# Error mapping: non-zero exit / not found / timeout
# ----------------------------------------------------------------------
class TestErrorMapping:
    def test_nonzero_exit_raises_command_error(self, client, mock_run):
        mock_run.result = (3, "partial stdout", "boom: replica missing")
        with pytest.raises(DfcacheCommandError) as excinfo:
            client.stat("t1")

        err = excinfo.value
        assert err.exit_code == 3
        assert err.stdout == "partial stdout"
        assert err.stderr == "boom: replica missing"
        assert isinstance(err, RuntimeError)
        assert "exit 3" in str(err)

    def test_exit_127_maps_to_not_found(self, client, mock_run):
        mock_run.result = (127, "", "")
        with pytest.raises(DfcacheNotFoundError):
            client.stat("t1")

    def test_exit_126_maps_to_not_found(self, client, mock_run):
        mock_run.result = (126, "", "permission denied")
        with pytest.raises(DfcacheNotFoundError):
            client.stat("t1")

    def test_file_not_found_error_raises_not_found(self, client, mock_run):
        mock_run.error = FileNotFoundError(2, "No such file")
        with pytest.raises(DfcacheNotFoundError):
            client.stat("t1")

    def test_timeout_expired_raises_timeout_error(self, client, mock_run):
        mock_run.error = subprocess.TimeoutExpired(cmd="dfcache", timeout=1.0)
        with pytest.raises(DfcacheTimeoutError):
            client.stat("t1")

    def test_zero_exit_returns_parsed_output(self, client, mock_run):
        mock_run.result = (0, '{"size": 1}', "warning noise")
        assert client.stat("t1") == {"size": 1}

    def test_missing_absolute_binary_path_raises_not_found(self, tmp_path):
        client = DfcacheClient(
            DfcacheConfig(binary_path=str(tmp_path / "no-such-dfcache.exe"))
        )
        with pytest.raises(DfcacheNotFoundError):
            client.stat("t1")
