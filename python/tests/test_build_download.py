"""Tests for building the Download request message.
"""

import datetime

from dragonfly_api import common_pb2

from dragonfly_sdk import DEFAULT_PRIORITY, build_download


def test_defaults_match_dfget():
    download = build_download("https://example.com/file", "/tmp/file")

    assert download.url == "https://example.com/file"
    assert download.output_path == "/tmp/file"
    assert download.type == common_pb2.STANDARD
    assert download.priority == DEFAULT_PRIORITY == common_pb2.LEVEL6
    assert not download.HasField("tag")
    assert not download.HasField("digest")


def test_optional_fields_are_set_when_provided():
    download = build_download(
        "https://example.com/file",
        "/tmp/file",
        tag="my-tag",
        application="my-app",
        digest="sha256:abc",
        piece_length=4 * 1024 * 1024,
        filtered_query_params=["Signature", "Expires"],
        request_header={"Accept": "application/json"},
        timeout=datetime.timedelta(seconds=30),
        overwrite=True,
    )

    assert download.tag == "my-tag"
    assert download.application == "my-app"
    assert download.digest == "sha256:abc"
    assert download.piece_length == 4 * 1024 * 1024
    assert list(download.filtered_query_params) == ["Signature", "Expires"]
    assert download.request_header["Accept"] == "application/json"
    assert download.timeout.seconds == 30
    assert download.overwrite is True
