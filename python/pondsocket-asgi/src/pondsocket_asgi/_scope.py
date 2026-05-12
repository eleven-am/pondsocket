from __future__ import annotations

from http.cookies import SimpleCookie
from typing import Any
from urllib.parse import parse_qs

from pondsocket.types import Route
from pondsocket_common import Headers, IncomingConnection


def parse_query(query_string: bytes) -> dict[str, str]:
    if not query_string:
        return {}
    qs = query_string.decode("utf-8", errors="replace")
    parsed = parse_qs(qs, keep_blank_values=True)
    return {k: v[0] if v else "" for k, v in parsed.items()}


def parse_cookies(headers: Headers) -> dict[str, str]:
    cookie_header = headers.get("cookie")
    if cookie_header is None:
        return {}
    jar: SimpleCookie = SimpleCookie()
    try:
        jar.load(cookie_header)
    except Exception:
        return {}
    return {key: morsel.value for key, morsel in jar.items()}


def client_address(scope: dict[str, Any]) -> str:
    client = scope.get("client")
    if client and isinstance(client, (list, tuple)) and len(client) >= 1:
        host = client[0]
        if isinstance(host, str):
            return host
    return ""


def build_incoming_connection(
    *,
    user_id: str,
    scope: dict[str, Any],
    route: Route,
) -> IncomingConnection:
    raw_headers = scope.get("headers", [])
    headers = Headers(raw_headers)
    return IncomingConnection(
        id=user_id,
        address=client_address(scope),
        headers=headers,
        cookies=parse_cookies(headers),
        query=parse_query(scope.get("query_string", b"")),
        params=dict(route.params),
    )
