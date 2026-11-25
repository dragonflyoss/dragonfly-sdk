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

"""Mirrored from the dragonfly client's `dragonfly-client-util/src/url/mod.rs`.
The canonicalization here decides the task id, so keep both copies in sync, any
divergence silently produces different task ids between the SDK and the client.

The scheduler canonicalizes with Go's net/url, so the parsing and the
serialization below follow net/url exactly: the path, the fragment, the
userinfo and the host are re-escaped the way url.URL.String does, the query is
rebuilt the way url.Values.Encode does, and the inputs net/url rejects raise.
Where the Rust url crate applies WHATWG normalizations that net/url does not
(lowercasing the host, dropping default ports, resolving dot segments) this
module keeps the url as written, like the scheduler.
"""

from __future__ import annotations

import ipaddress
from collections.abc import Sequence
from dataclasses import dataclass
from enum import Enum

from .errors import InvalidArgumentError

__all__ = ["filter_query_params"]


class _Encoding(Enum):
    """The component of the url a byte is escaped for, which decides the
    characters that are kept as is, like the encoding modes of net/url.
    """

    PATH = "path"
    USER_PASSWORD = "user_password"
    QUERY_COMPONENT = "query_component"
    FRAGMENT = "fragment"
    HOST = "host"
    ZONE = "zone"


# The unreserved marks, never escaped in any component.
_MARKS = frozenset(b"-_.~")

# The reserved characters, escaped or not depending on the component.
_RESERVED = frozenset(b"$&+,/:;=?@")

# The sub-delims and the port and address literal characters a host keeps.
_HOST_KEPT = frozenset(b"!$&'()*+,;=:[]<>\"")

# The sub-delims a fragment keeps besides the reserved characters.
_FRAGMENT_KEPT = frozenset(b"!()*")

# The characters a validly encoded path or fragment may contain unescaped
# besides the ones the component never escapes.
_ENCODED_KEPT = frozenset(b"!$&'()*+,;=:@[]%")

# The characters the userinfo may contain.
_USERINFO_CHARS = frozenset(b"-._:~!$&'()*+,;=%@")

_HEX_DIGITS = frozenset(b"0123456789abcdefABCDEF")


def _is_alphanumeric(c: int) -> bool:
    return 0x30 <= c <= 0x39 or 0x41 <= c <= 0x5A or 0x61 <= c <= 0x7A


def _should_escape(c: int, mode: _Encoding) -> bool:
    """Reports whether the byte must be percent-encoded in the component."""
    if _is_alphanumeric(c):
        return False

    if mode in (_Encoding.HOST, _Encoding.ZONE) and c in _HOST_KEPT:
        return False

    if c in _MARKS:
        return False

    if c in _RESERVED:
        if mode is _Encoding.PATH:
            return c == 0x3F  # ?
        if mode is _Encoding.USER_PASSWORD:
            return c in b"@/?:"
        if mode is _Encoding.QUERY_COMPONENT:
            return True
        if mode is _Encoding.FRAGMENT:
            return False

    return not (mode is _Encoding.FRAGMENT and c in _FRAGMENT_KEPT)


def _unescape(s: bytes, mode: _Encoding) -> bytes:
    """Decodes the percent-encoded bytes of the component, rejecting malformed
    escapes and, in a host, the characters and the escaped ASCII bytes a host
    may not contain.
    """
    unescaped = bytearray()
    i = 0
    while i < len(s):
        c = s[i]
        if c == 0x25:  # %
            if i + 2 >= len(s) or s[i + 1] not in _HEX_DIGITS or s[i + 2] not in _HEX_DIGITS:
                raise ValueError(f"invalid escape {s[i : i + 3]!r}")

            escaped = s[i : i + 3]
            value = int(escaped[1:], 16)
            if mode is _Encoding.HOST and value < 0x80 and escaped != b"%25":
                raise ValueError(f"invalid escape {escaped!r}")

            if (
                mode is _Encoding.ZONE
                and escaped != b"%25"
                and value != 0x20
                and _should_escape(value, _Encoding.HOST)
            ):
                raise ValueError(f"invalid escape {escaped!r}")

            unescaped.append(value)
            i += 3
        elif c == 0x2B:  # +
            unescaped.append(0x20 if mode is _Encoding.QUERY_COMPONENT else c)
            i += 1
        else:
            if mode in (_Encoding.HOST, _Encoding.ZONE) and c < 0x80 and _should_escape(c, mode):
                raise ValueError(f"invalid character {chr(c)!r} in host name")

            unescaped.append(c)
            i += 1

    return bytes(unescaped)


def _escape(s: bytes, mode: _Encoding) -> bytes:
    """Percent-encodes the bytes the component must escape, a space becoming a
    plus in a query component.
    """
    escaped = bytearray()
    for c in s:
        if c == 0x20 and mode is _Encoding.QUERY_COMPONENT:
            escaped += b"+"
        elif _should_escape(c, mode):
            escaped += b"%%%02X" % c
        else:
            escaped.append(c)

    return bytes(escaped)


def _valid_encoded(s: bytes, mode: _Encoding) -> bool:
    """Reports whether the component is already a valid encoding, in which case
    it is kept as written.
    """
    return all(c in _ENCODED_KEPT or not _should_escape(c, mode) for c in s)


def _escaped(s: bytes, mode: _Encoding) -> bytes:
    """Returns the path or the fragment as it is serialized, kept as written
    when validly encoded and re-escaped otherwise.
    """
    unescaped = _unescape(s, mode)
    return s if _valid_encoded(s, mode) else _escape(unescaped, mode)


def _valid_optional_port(port: bytes) -> bool:
    return not port or (port[0] == 0x3A and all(0x30 <= c <= 0x39 for c in port[1:]))


def _parse_host(host: bytes) -> bytes:
    """Validates the host and decodes its escapes. An address literal in
    brackets must be an IPv6 address, optionally with a zone introduced by
    `%25`, and a port must be decimal.
    """
    open_bracket = host.rfind(b"[")
    if open_bracket != -1:
        close_bracket = host.rfind(b"]")
        if close_bracket < 0:
            raise ValueError("missing ']' in host")

        colon_port = host[close_bracket + 1 :]
        if not _valid_optional_port(colon_port):
            raise ValueError(f"invalid port {colon_port!r} after host")

        hostname = host[open_bracket + 1 : close_bracket]
        zone = hostname.find(b"%25")
        if zone >= 0:
            hostname = _unescape(hostname[:zone], _Encoding.HOST) + _unescape(
                hostname[zone:], _Encoding.ZONE
            )
        else:
            hostname = _unescape(hostname, _Encoding.HOST)

        try:
            ipaddress.IPv6Address(hostname.decode())
        except (UnicodeDecodeError, ValueError) as err:
            raise ValueError(f"invalid host: {err}") from err

        return b"[" + hostname + b"]" + _unescape(colon_port, _Encoding.HOST)

    colon = host.rfind(b":")
    if colon != -1 and not _valid_optional_port(host[colon:]):
        raise ValueError(f"invalid port {host[colon:]!r} after host")

    return _unescape(host, _Encoding.HOST)


def _parse_userinfo(userinfo: bytes) -> bytes:
    """Validates the userinfo and returns it re-escaped as it is serialized."""
    if not all(_is_alphanumeric(c) or c in _USERINFO_CHARS for c in userinfo):
        raise ValueError("invalid userinfo")

    if b":" not in userinfo:
        return _escape(_unescape(userinfo, _Encoding.USER_PASSWORD), _Encoding.USER_PASSWORD)

    username, _, password = userinfo.partition(b":")
    return (
        _escape(_unescape(username, _Encoding.USER_PASSWORD), _Encoding.USER_PASSWORD)
        + b":"
        + _escape(_unescape(password, _Encoding.USER_PASSWORD), _Encoding.USER_PASSWORD)
    )


def _split_scheme(url: bytes) -> tuple[bytes, bytes]:
    """Splits the scheme off the url, returning an empty scheme when the url
    does not start with one.
    """
    for i, c in enumerate(url):
        if 0x41 <= c <= 0x5A or 0x61 <= c <= 0x7A:
            continue

        if 0x30 <= c <= 0x39 or c in b"+-.":
            if i == 0:
                return b"", url
        elif c == 0x3A:  # :
            if i == 0:
                raise ValueError("missing protocol scheme")

            return url[:i], url[i + 1 :]
        else:
            return b"", url

    return b"", url


@dataclass
class _URL:
    """The components of a parsed url, each already escaped as it is
    serialized, except the host which is escaped on serialization and the
    query which is rebuilt from its filtered parameters.
    """

    scheme: bytes = b""
    opaque: bytes = b""
    userinfo: bytes | None = None
    host: bytes = b""
    path: bytes = b""
    query: bytes = b""
    fragment: bytes = b""

    # force_query keeps the trailing `?` of a url with an empty query.
    force_query: bool = False

    # omit_host marks a url with a rooted path and no authority, such as
    # `file:/path`, so no empty authority is serialized.
    omit_host: bool = False

    def __str__(self) -> str:
        serialized = self.scheme + b":"
        if self.opaque:
            serialized += self.opaque
        else:
            if not (self.omit_host and not self.host and self.userinfo is None):
                if self.host or self.path or self.userinfo is not None:
                    serialized += b"//"

                if self.userinfo is not None:
                    serialized += self.userinfo + b"@"

                serialized += _escape(self.host, _Encoding.HOST)

            serialized += self.path

        if self.force_query or self.query:
            serialized += b"?" + self.query

        if self.fragment:
            serialized += b"#" + self.fragment

        return serialized.decode()


def _parse(url: str) -> _URL:
    """Parses the url the way the scheduler does, raising on the inputs it
    rejects. A url without a scheme is rejected as well, since it cannot be
    downloaded.
    """
    raw, _, fragment = url.encode().partition(b"#")
    if any(c < 0x20 or c == 0x7F for c in raw):
        raise ValueError("invalid control character in url")

    parsed = _URL()
    scheme, rest = _split_scheme(raw)
    if not scheme:
        raise ValueError("missing protocol scheme")

    parsed.scheme = scheme.lower()
    if rest.endswith(b"?") and rest.count(b"?") == 1:
        parsed.force_query = True
        rest = rest[:-1]
    else:
        rest, _, parsed.query = rest.partition(b"?")

    if fragment:
        parsed.fragment = _escaped(fragment, _Encoding.FRAGMENT)

    if not rest.startswith(b"/"):
        parsed.opaque = rest
        return parsed

    if rest.startswith(b"//"):
        authority, slash, path = rest[2:].partition(b"/")
        at = authority.rfind(b"@")
        parsed.host = _parse_host(authority[at + 1 :])
        if at >= 0:
            parsed.userinfo = _parse_userinfo(authority[:at])

        rest = slash + path
    else:
        parsed.omit_host = True

    parsed.path = _escaped(rest, _Encoding.PATH)
    return parsed


def _filter_query(query: bytes, filtered_query_params: Sequence[str]) -> bytes:
    """Decodes the query parameters, drops the filtered ones and the segments
    the scheduler drops (a segment containing a semicolon or a malformed
    escape), and encodes them back sorted by key, the values of the same key
    keeping their order.
    """
    hidden = {param.encode() for param in filtered_query_params}
    values: dict[bytes, list[bytes]] = {}
    while query:
        segment, _, query = query.partition(b"&")
        if not segment or b";" in segment:
            continue

        key, _, value = segment.partition(b"=")
        try:
            key = _unescape(key, _Encoding.QUERY_COMPONENT)
            value = _unescape(value, _Encoding.QUERY_COMPONENT)
        except ValueError:
            continue

        if key not in hidden:
            values.setdefault(key, []).append(value)

    return b"&".join(
        _escape(key, _Encoding.QUERY_COMPONENT) + b"=" + _escape(value, _Encoding.QUERY_COMPONENT)
        for key in sorted(values)
        for value in values[key]
    )


def filter_query_params(url: str, filtered_query_params: Sequence[str]) -> str:
    """Filters and sorts the query parameters by key to canonicalize the url,
    following the scheduler's canonicalization (Go's url.Values.Encode). The
    values of the same key keep the original order. Segments containing a
    semicolon are dropped, like Go's url.ParseQuery.
    """
    if not filtered_query_params:
        return url

    try:
        parsed = _parse(url)
    except ValueError as err:
        raise InvalidArgumentError(f"invalid url: {err}") from err

    parsed.query = _filter_query(parsed.query, filtered_query_params)
    return str(parsed)
