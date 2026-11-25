#     Copyright 2026 The Dragonfly Authors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#      http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Mirrors d7y.io/dragonfly/v2 pkg/net/url url_test.go TestFilterQuery and the
# Rust crate's src/url.rs unit tests. The GO_PINNED vectors were produced by
# running d7y.io/dragonfly/v2@v2.5.2-rc.3 pkg/net/url.FilterQueryParams and
# idgen.TaskIDV2 directly; they pin the inputs on which a WHATWG parser such as
# the Rust url crate canonicalizes differently from the scheduler's net/url.

import pytest

from dragonfly_client_request.errors import InvalidArgumentError
from dragonfly_client_request.url import _Encoding, _escape, filter_query_params


# Rust url.rs should_escape_query.
@pytest.mark.parametrize(
    ("s", "expected"),
    [
        ("a b", "a+b"),
        ("x*y", "x%2Ay"),
        ("c~d", "c~d"),
        ("1+1", "1%2B1"),
        ("中", "%E4%B8%AD"),
    ],
)
def test_query_escape(s: str, expected: str) -> None:
    assert _escape(s.encode(), _Encoding.QUERY_COMPONENT).decode() == expected


# Rust url.rs should_filter_query_params, a superset of Go url_test.go.
@pytest.mark.parametrize(
    ("url", "filtered_query_params", "expected"),
    [
        ("https://example.com/file.txt?z=9&b=2&a=1", ["z"], "https://example.com/file.txt?a=1&b=2"),
        (
            "https://example.com/file.txt?b=2&a=1&b=1",
            ["c"],
            "https://example.com/file.txt?a=1&b=2&b=1",
        ),
        ("https://example.com?foo=foo", ["foo"], "https://example.com"),
        ("https://example.com?foo=foo&bar=bar", ["foo"], "https://example.com?bar=bar"),
        ("https://example.com?foo=foo#size", ["foo"], "https://example.com#size"),
        (
            "https://user:pass@[::1]:8080?foo=foo&bar=bar",
            ["foo"],
            "https://user:pass@[::1]:8080?bar=bar",
        ),
        ("https://example.com/?foo=foo", ["foo"], "https://example.com/"),
        ("https://example.com/?foo=foo&bar=bar", ["foo"], "https://example.com/?bar=bar"),
        (
            "https://example.com/file.txt?k=a b&m=x*y&n=c~d",
            ["none"],
            "https://example.com/file.txt?k=a+b&m=x%2Ay&n=c~d",
        ),
        (
            "http://www.xx.yy/path?u=f&x=y&m=z&x=s#size",
            ["x", "m"],
            "http://www.xx.yy/path?u=f#size",
        ),
        (
            "http://www.xx.yy/path?u=f&x=y&m=z&x=s#size",
            [],
            "http://www.xx.yy/path?u=f&x=y&m=z&x=s#size",
        ),
        ("https://example.com/file.txt?a=1;x&b=2", ["none"], "https://example.com/file.txt?b=2"),
    ],
)
def test_filter_query_params(url: str, filtered_query_params: list[str], expected: str) -> None:
    assert filter_query_params(url, filtered_query_params) == expected


# The inputs net/url rejects, on which Go's TaskIDV2ByURLBased swallows the
# error and hashes an empty url. The Python module follows Rust and raises, so
# a task id never silently collides on the empty url. A url without a scheme is
# rejected as well, net/url parses it as a relative path.
@pytest.mark.parametrize(
    "url",
    [
        ":error_url",
        "",
        "not-a-url",
        "/path?a=1",
        "example.com/path?a=1",
        "http://[::1/x?a=1",
        "http://[1.2.3.4]/x?a=1",
        "http://[zz]/x?a=1",
        "http://[::1]:abc/x?a=1",
        "http://[fe80::1%eth0]/x?a=1",
        "http://x:abc/p?a=1",
        "http://a b.com/p?a=1",
        "http://exam%41ple.com/p?a=1",
        "https://user:pa ss@example.com/p?a=1",
        "https://用户@example.com/p?a=1",
        "https://example.com/p%?a=1",
        "https://example.com/p%2?a=1",
        "https://example.com/p%zz?a=1",
        "https://example.com/p?a=1#a%zz",
        "https://example.com/p\x01?a=1",
        "https://example.com/p?a=1\x7f",
    ],
)
def test_filter_query_params_rejects_invalid_url(url: str) -> None:
    with pytest.raises(InvalidArgumentError):
        filter_query_params(url, ["x", "m"])


# Vectors generated from the Go scheduler's own canonicalization
# (d7y.io/dragonfly/v2@v2.5.2-rc.3). Each pins a place where a WHATWG parser
# would normalize differently, or where net/url re-escapes a component.
GO_PINNED = [
    # The host, the port and the dot segments are kept as written.
    (
        "https://EXAMPLE.com/File.txt?z=9&a=1",
        ["z"],
        "https://EXAMPLE.com/File.txt?a=1",
        "77ed08fb978ee098f684e7be7bc48f9c38d38918140655b137882fb216823a3d",
    ),
    (
        "https://example.com:443/file.txt?z=9&a=1",
        ["z"],
        "https://example.com:443/file.txt?a=1",
        "0a2646e65d40190f4647e1ee99d0b8166ded8268ce52c8494bda5b77bdfe95ba",
    ),
    (
        "http://example.com:80/file.txt?z=9&a=1",
        ["z"],
        "http://example.com:80/file.txt?a=1",
        "6b5595ad7b0db4f687cca4904c9cfd0bd75357a3b9148900e112df5f7058f7f2",
    ),
    (
        "https://example.com/a/../b/./file.txt?z=9&a=1",
        ["z"],
        "https://example.com/a/../b/./file.txt?a=1",
        "7a3cd088d337167d9d0b1d8530a0d8474cafb58d1da653be5f82d33b76523c1c",
    ),
    (
        "https://example.com:/p?a=1",
        ["a"],
        "https://example.com:/p",
        "482951c32e94a4e6dc2c36c733ba68b46f1c23635fcaa4f4745e70df613701cc",
    ),
    (
        "https://a:b:80/p?a=1",
        ["a"],
        "https://a:b:80/p",
        "88c4bb7d260b30acf1493e8ae68bb9fc655d29861f7615ca27ced92d51c5bb5e",
    ),
    (
        "HTTPS://example.com/file.txt?z=9&a=1",
        ["z"],
        "https://example.com/file.txt?a=1",
        "20fba74dee4e61d574a5527b4957a8d6844416195bd94232effd184bd9d9bbe2",
    ),
    # A validly encoded path is kept as written, any other path is re-escaped.
    (
        "https://example.com/a%2Fb/file.txt?z=9&a=1",
        ["z"],
        "https://example.com/a%2Fb/file.txt?a=1",
        "24b81f0d37d02872d5ed92c550d4fcaba9e36bcaf3ed6f7afbe85b9e901aab15",
    ),
    (
        "https://example.com/a%20b?a=1",
        ["a"],
        "https://example.com/a%20b",
        "6d852f0e6a50d883a2a8178791117fb92d5324a9bd077f0cb08d1585fe4c8532",
    ),
    (
        "https://example.com/a%41?a=1",
        ["a"],
        "https://example.com/a%41",
        "3b05192144b0ffabec27a921ee2768a7bd6f83da4069f0e505a29033b3ff3546",
    ),
    (
        "https://example.com/%7Ea?a=1",
        ["a"],
        "https://example.com/%7Ea",
        "07e74857438e263869ea48ca434a5099c10cc9aea2b792dbaaf9385e23d087c1",
    ),
    (
        "https://example.com/a'b?a=1",
        ["a"],
        "https://example.com/a'b",
        "e0fac2fa04960a544895bb3622f5a629e7a502f17d013edfc20d4698e045b195",
    ),
    (
        "https://example.com/key with space.tar?a=1",
        ["a"],
        "https://example.com/key%20with%20space.tar",
        "e63912f3db2116c91ee0479fecf56bc4d69dbf23f2c16f91a2e0e71773967325",
    ),
    (
        "https://bucket.s3.amazonaws.com/path/to/文件.tar?X-Amz-Signature=abc&X-Amz-Date=1",
        ["X-Amz-Signature", "X-Amz-Date"],
        "https://bucket.s3.amazonaws.com/path/to/%E6%96%87%E4%BB%B6.tar",
        "2a2765926f83f830ffab20734437c63d1a8060be7ab3c91548b5e7798be6a62a",
    ),
    (
        "https://example.com/🙂/p?a=1",
        ["a"],
        "https://example.com/%F0%9F%99%82/p",
        "23899f58e9e234984974de71e50f721747c3b47c3e4c979f4c399a6065aa518e",
    ),
    (
        'https://example.com/a"b<c>d{e}f|g^h`i\\j?a=1',
        ["a"],
        "https://example.com/a%22b%3Cc%3Ed%7Be%7Df%7Cg%5Eh%60i%5Cj",
        "efc72541624d6e3d0a9ecad7e515657f26a932c4f2e124caebd5ade615470777",
    ),
    (
        "s3://bucket/a b/c.tar?a=1",
        ["a"],
        "s3://bucket/a%20b/c.tar",
        "31e827726c18389b1e184bbd9641cbfd76fab7dcd0ec9e50a27bd41674126a7f",
    ),
    # The fragment is re-escaped the same way, keeping the sub-delims.
    (
        "https://example.com/file.txt?z=9&a=1#frag",
        ["z"],
        "https://example.com/file.txt?a=1#frag",
        "ef4393cb412fe4768882c4936c7e1e52c83d5024000165a07fa297cc5be6c977",
    ),
    (
        "https://example.com/p?a=1#frag ment",
        ["a"],
        "https://example.com/p#frag%20ment",
        "e84daf90e20a8dc7d9b7e89d56202d7bed4bfb1afd65be87feb901b587539712",
    ),
    (
        "https://example.com/p?a=1#中",
        ["a"],
        "https://example.com/p#%E4%B8%AD",
        "0d189a84df7215a26e508808d45380fab42000f20fa7c9bed1ec49c1704d274d",
    ),
    (
        "https://example.com/p?a=1#a'b",
        ["a"],
        "https://example.com/p#a'b",
        "04e9cabe0fb5214c5df85c3084b3f6628dfd087fddc5139f068dff3c44714b11",
    ),
    (
        "https://example.com/p?a=1#!()*",
        ["a"],
        "https://example.com/p#!()*",
        "b32e50d4b30a664f039188e247dbfafc21dcbd0ee8b6c5f25d8e6f408b59b752",
    ),
    (
        "https://example.com/p?a=1#%41",
        ["a"],
        "https://example.com/p#%41",
        "9349fe6bc98fc44f2af17a625452c7e93138170f768fc225f434a99bcbaf1ff1",
    ),
    (
        "https://example.com/p?a=1#a#b",
        ["a"],
        "https://example.com/p#a%23b",
        "ec1e7a804bc8c526b40fadfc08c9187170cd4a6b71182e61e2d3561b6e1591f4",
    ),
    (
        "https://example.com/p?a=1#?b=2",
        ["a"],
        "https://example.com/p#?b=2",
        "402d09f3b52dff3eddb1c9e2a205ce5a61f033f3eb058e1b7bb70a5c6d2070de",
    ),
    (
        "https://example.com/p?a=1#",
        ["a"],
        "https://example.com/p",
        "41b21a6f515f81da8a797c380cdcf25e719f315a45ba8316221ec4be62641c91",
    ),
    # A non-ASCII host is percent-encoded, an escaped non-ASCII host is kept.
    (
        "https://例子.com/p?a=1",
        ["a"],
        "https://%E4%BE%8B%E5%AD%90.com/p",
        "d658367bb54a1c7fb2490e88da40f5f39dddda4f084a512df33d977e917e5574",
    ),
    (
        "https://exam%C3%A9ple.com/p?a=1",
        ["a"],
        "https://exam%C3%A9ple.com/p",
        "fdc303dd8c253978fcff8895ed3487f68eb0b2e675f3ffc74b30867fdc75e0b4",
    ),
    (
        "https://[::1]:8080/file.txt?z=9&a=1",
        ["z"],
        "https://[::1]:8080/file.txt?a=1",
        "f088d9a4d3ef6a5f3928d1f503de22ee88482d228cf3b318e72d8b2aff60fb1e",
    ),
    (
        "https://[fe80::1%25eth0]/p?a=1",
        ["a"],
        "https://[fe80::1%25eth0]/p",
        "e566b83195f38460d51cd53b63667c0eb18df25e97eb5b8e36a8ab6dfe51916b",
    ),
    (
        "https://[::ffff:1.2.3.4]/p?a=1",
        ["a"],
        "https://[::ffff:1.2.3.4]/p",
        "2c16eb6f9ffc2789c3787d6edb9ab112478f9a4fb73e6106eb92c11a56c9056f",
    ),
    (
        "https://127.0.0.1:65000/file.txt?z=9&a=1",
        ["z"],
        "https://127.0.0.1:65000/file.txt?a=1",
        "91cb286d33424505f5a95af286384aa1b3ac068513e16c04cff56789e9834dc5",
    ),
    # The userinfo is decoded and re-escaped.
    (
        "https://user:pass@example.com/file.txt?z=9&a=1",
        ["z"],
        "https://user:pass@example.com/file.txt?a=1",
        "898cb95f4bc1a2504c85a858f3512dd5249e59c42ad3a688ff9a3390ca7ff9bc",
    ),
    (
        "https://us%41er:p%40ss@example.com/p?a=1",
        ["a"],
        "https://usAer:p%40ss@example.com/p",
        "cd1f95ab6b4297f25524fc4e14e497c01cca36a69318c94834526f086bf59bbf",
    ),
    (
        "https://user:p@ss@example.com/p?a=1",
        ["a"],
        "https://user:p%40ss@example.com/p",
        "8d339c747c4aa9b4081d571ab834753b6e5fa81e3ccff868c36b6f8037a18468",
    ),
    (
        "https://user:pass:more@example.com/p?a=1",
        ["a"],
        "https://user:pass%3Amore@example.com/p",
        "cb74c769efdc9b7129a733b0adc2630799507bd5d8f53dd710312eb0cb615ef7",
    ),
    (
        "https://user:@example.com/p?a=1",
        ["a"],
        "https://user:@example.com/p",
        "48ef563df9221765b84ceeb71edab098fb6192176e44170227abb1a3bf1893ab",
    ),
    (
        "https://:@example.com/p?a=1",
        ["a"],
        "https://:@example.com/p",
        "226a069660efe4e23175e3a5bb9fbe2cab03913ab516e754f835e85ef36de1ba",
    ),
    # The query is decoded, sorted by key bytewise and re-encoded. A segment
    # with a malformed escape is dropped, a trailing `?` is kept.
    (
        "https://example.com/file.txt?a=%41&b=%2b",
        ["none"],
        "https://example.com/file.txt?a=A&b=%2B",
        "4c101ddd99696e3f3567d69d7efc57ec6492b1a0262aa490639fdff9cc81a23e",
    ),
    (
        "https://example.com/file.txt?a=1&a=1&b",
        ["none"],
        "https://example.com/file.txt?a=1&a=1&b=",
        "723544e15d83fedd91e286ff66474bd96087820c96e9d4c02ba81068260a9719",
    ),
    (
        "https://example.com/file.txt?=v&k=",
        ["none"],
        "https://example.com/file.txt?=v&k=",
        "60a0ab5e80eb0861e03efa891b96d259878382e0d110d8a30aed122181be24c2",
    ),
    (
        "https://example.com/p?a=1&a=2&A=3&b=&=c&d",
        ["none"],
        "https://example.com/p?=c&A=3&a=1&a=2&b=&d=",
        "1b693bcd5c61a46c8190daa3fab5882bcf8eb89c75c83414c00956181d648149",
    ),
    (
        "https://example.com/file.txt?k=a%20b",
        ["none"],
        "https://example.com/file.txt?k=a+b",
        "74fcd5db0c0524a631398a3f674edfd044632d827c0ff3b228147ae8920dfc3f",
    ),
    (
        "https://example.com/file.txt?中=文",
        ["none"],
        "https://example.com/file.txt?%E4%B8%AD=%E6%96%87",
        "ec3c16c7f813e6988e705531f8d289949ac81a052d7845130dc9bb5f2017b0fd",
    ),
    (
        "https://example.com/file.txt?a=%E4%B8%AD",
        ["none"],
        "https://example.com/file.txt?a=%E4%B8%AD",
        "ab9412382964cbf079df5d08023d693654ba6802017913c453be82824e9e68d7",
    ),
    (
        "https://example.com/file.txt?a=中",
        ["none"],
        "https://example.com/file.txt?a=%E4%B8%AD",
        "ab9412382964cbf079df5d08023d693654ba6802017913c453be82824e9e68d7",
    ),
    (
        "https://example.com/p?a=%FF",
        ["none"],
        "https://example.com/p?a=%FF",
        "8dd6ba53874111fc98801c1e56d2b6325bef80a698f655a0b17ff62cc3828fdd",
    ),
    (
        "https://example.com/file.txt?a=+",
        ["none"],
        "https://example.com/file.txt?a=+",
        "a3b03bd223a07ae3c9cb8df070c3c33d28692509d4a3f0d215a63a8b2e408c6f",
    ),
    (
        "https://example.com/file.txt?a=%2B",
        ["none"],
        "https://example.com/file.txt?a=%2B",
        "7826006258054af012a23fa24ae2a2cb00e1cc0fa02e26fd58eaf4b57385a66b",
    ),
    (
        "https://example.com/file.txt?a%3Bb=1&c=2",
        ["none"],
        "https://example.com/file.txt?a%3Bb=1&c=2",
        "32f03f027c52ac2f40a3b5ae4a7aa3dd8c92d4264c0c4513ab89f5f24fde4684",
    ),
    (
        "https://example.com/file.txt?a=1%262&c=2",
        ["none"],
        "https://example.com/file.txt?a=1%262&c=2",
        "d5b7c503afc8123cb93157b66bfa135263a984fcf9a160c603113c5edc0dbc51",
    ),
    (
        "https://example.com/file.txt?a=1#frag?x=1",
        ["none"],
        "https://example.com/file.txt?a=1#frag?x=1",
        "d4177a0642261156874a3695a991333529a71d355dd1c63a750d2f7d3d87fb99",
    ),
    (
        "https://example.com/p?a=1?b=2",
        ["none"],
        "https://example.com/p?a=1%3Fb%3D2",
        "fba36a31fae4b12a2272a913a1d1f163200bdeb930c3a80d0a284cb732e80cd4",
    ),
    (
        "https://example.com/p?a==1",
        ["none"],
        "https://example.com/p?a=%3D1",
        "1efb694e1d52df9dba817d4b9d3c21cbc7a0f599b60ac3ed63f5715b080f27b7",
    ),
    (
        "https://example.com/p?a=%zz&b=1",
        ["none"],
        "https://example.com/p?b=1",
        "99ad770d6462d89a013d62ec5d876f4cd76caa93f98655401bc3fe00e80cbb10",
    ),
    (
        "https://example.com/p?a=1&%zz=2",
        ["none"],
        "https://example.com/p?a=1",
        "5190b1d2f6968acfc6d8b20c631e7c4c5a66eb3c4e97b80ffc7941acb53300fc",
    ),
    (
        "https://example.com/file.txt?a=1&&b=2",
        ["none"],
        "https://example.com/file.txt?a=1&b=2",
        "e357ada0452a4d289ab572db4352a542174a971e96360df15b7fd2b1ef636d18",
    ),
    (
        "https://example.com/p?",
        ["a"],
        "https://example.com/p?",
        "dfadda09fc8a14f3684a0ed9630df85a2353a2c8a5bd84c59b464bdfe7101a12",
    ),
    (
        "https://example.com/p??",
        ["a"],
        "https://example.com/p?%3F=",
        "3fd9dd73fa9c96eef2edc96b9afc3d44c98ef2fd0c0f4f89057f4fd632e0afd2",
    ),
    (
        "https://example.com?#",
        ["a"],
        "https://example.com?",
        "7e913f5b0e92fcf51f586b3c07e9a728a6fc7c26bdeca5ff82d4b2c5754c0f64",
    ),
    (
        "https://example.com/file.txt?z=9&a=1",
        ["Z"],
        "https://example.com/file.txt?a=1&z=9",
        "720075b187fa247d03f7601e5bcd9d5f38d94f0fcfa2a780922b7a8eef02c89f",
    ),
    (
        "https://example.com/file.txt?B=2&a=1&b=3",
        ["none"],
        "https://example.com/file.txt?B=2&a=1&b=3",
        "7996c3f2ce7e83d96fa355f4c7a9bbc939174ceb2b3753962a213cf45e3058f8",
    ),
    (
        "https://example.com/file.txt?a=1&a=1",
        ["none"],
        "https://example.com/file.txt?a=1&a=1",
        "acb1f791f25f30fd6419d6b1bcf3f493be4b38b6ae8119f6e3a3ec737e7ee97c",
    ),
    (
        "https://example.com/file.txt?k=v&Expires=1&Signature=2",
        ["Expires", "Signature"],
        "https://example.com/file.txt?k=v",
        "dbf15f74ee2507f4e781005af18ae599b94a9f99900e33c4aeb1ace789fffbee",
    ),
    (
        "https://example.com/file.txt?a=1",
        ["a"],
        "https://example.com/file.txt",
        "7fcf06e5f0b1e443065c1a563eed788eb2e168a05c6ad9c4b319f7a976322be0",
    ),
    # An empty path, an empty or omitted authority and an opaque url.
    (
        "https://example.com?z=9&a=1",
        ["z"],
        "https://example.com?a=1",
        "6395a89461ed9da6597da611e2987df5663fa0d96ff9219de7bb4b5c2bed0bdc",
    ),
    (
        "https://example.com/?z=9&a=1",
        ["z"],
        "https://example.com/?a=1",
        "3fcf5703e74d955da85a442ec305c13163e496df369db49d790573b45f37396f",
    ),
    (
        "https:///p?a=1",
        ["a"],
        "https:///p",
        "4dbca07d18e9cf659cf0fea6f31a8cb96d023e36ce6cb9264cef26228cc47ba5",
    ),
    (
        "https://?a=1",
        ["a"],
        "https:",
        "eb0842a8051569265c65aecb8d711464b817ea83146055639f2c131eca3618bb",
    ),
    (
        "file:///tmp/a b.txt?a=1",
        ["a"],
        "file:///tmp/a%20b.txt",
        "2b97ba25c0daecc140304e8b71b0804a7f4f87a38a31629b285355071dabd6f9",
    ),
    (
        "file:/tmp/a.txt?a=1",
        ["a"],
        "file:/tmp/a.txt",
        "7430b35004a53e6ffd9ac32ef59b869904f2333b5e2532961e4788573632754d",
    ),
    (
        "mailto:user@example.com?a=1&subject=x",
        ["a"],
        "mailto:user@example.com?subject=x",
        "1b4d19ef1c19e4ab668340455ebc7336f70ab12437f3809f75484fd6c65d6892",
    ),
    (
        "ftp://example.com/file.txt?z=9&a=1",
        ["z"],
        "ftp://example.com/file.txt?a=1",
        "549bdb8e2c31956b2749ce54b13ba3440139fca32eeb3a2682dc3f2b6f441a44",
    ),
    (
        "s3://bucket/key.txt?z=9&a=1",
        ["z"],
        "s3://bucket/key.txt?a=1",
        "c46473916a9d25439c9b0263c7021ec93eb779d2e149cc33364583941a416005",
    ),
]


@pytest.mark.parametrize(
    ("url", "filtered_query_params", "expected_url"),
    [(url, params, expected_url) for url, params, expected_url, _ in GO_PINNED],
)
def test_filter_query_params_matches_go_scheduler(
    url: str, filtered_query_params: list[str], expected_url: str
) -> None:
    assert filter_query_params(url, filtered_query_params) == expected_url
