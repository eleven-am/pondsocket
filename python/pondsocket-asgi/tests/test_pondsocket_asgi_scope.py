from __future__ import annotations

from pondsocket.types import Route
from pondsocket_asgi._scope import (
    build_incoming_connection,
    client_address,
    parse_cookies,
    parse_query,
)
from pondsocket_common import Headers


def test_parse_query_empty() -> None:
    assert parse_query(b"") == {}


def test_parse_query_single_pair() -> None:
    assert parse_query(b"token=abc") == {"token": "abc"}


def test_parse_query_multiple_pairs_picks_first() -> None:
    assert parse_query(b"a=1&a=2&b=3") == {"a": "1", "b": "3"}


def test_parse_query_url_decoded() -> None:
    assert parse_query(b"q=hello%20world") == {"q": "hello world"}


def test_parse_cookies_empty() -> None:
    headers = Headers()
    assert parse_cookies(headers) == {}


def test_parse_cookies_parses_basic_cookies() -> None:
    headers = Headers([(b"cookie", b"sid=abc; theme=dark")])
    assert parse_cookies(headers) == {"sid": "abc", "theme": "dark"}


def test_client_address_pulls_host_from_tuple() -> None:
    assert client_address({"client": ("10.0.0.1", 4567)}) == "10.0.0.1"


def test_client_address_returns_empty_when_missing() -> None:
    assert client_address({}) == ""
    assert client_address({"client": None}) == ""


def test_build_incoming_connection_assembles_all_fields() -> None:
    scope = {
        "client": ("1.2.3.4", 5678),
        "headers": [(b"x-token", b"abc"), (b"cookie", b"sid=xyz")],
        "query_string": b"token=abc&debug=1",
    }
    route = Route(params={"room_id": "42"})
    conn = build_incoming_connection(user_id="u-1", scope=scope, route=route)
    assert conn.id == "u-1"
    assert conn.address == "1.2.3.4"
    assert conn.headers.get("x-token") == "abc"
    assert conn.cookies == {"sid": "xyz"}
    assert conn.query == {"token": "abc", "debug": "1"}
    assert conn.params == {"room_id": "42"}
