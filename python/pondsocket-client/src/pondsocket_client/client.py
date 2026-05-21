from __future__ import annotations

from typing import Any
from urllib.parse import urlencode, urlsplit, urlunsplit

from ._websocket import WebSocketClient
from .types import ClientOptions


class PondClient(WebSocketClient):
    __slots__ = ("_endpoint", "_params")

    def __init__(
        self,
        endpoint: str,
        params: dict[str, Any] | None = None,
        options: ClientOptions | None = None,
    ) -> None:
        super().__init__(options)
        self._endpoint = endpoint
        self._params = dict(params or {})

    def _resolve_url(self) -> str:
        normalized = _normalize_ws_scheme(self._endpoint)
        if not self._params:
            return normalized
        parts = urlsplit(normalized)
        existing = parts.query
        merged = urlencode(self._params, doseq=True)
        combined = f"{existing}&{merged}" if existing else merged
        return urlunsplit(
            (parts.scheme, parts.netloc, parts.path, combined, parts.fragment)
        )


def _normalize_ws_scheme(url: str) -> str:
    parts = urlsplit(url)
    scheme = parts.scheme.lower()
    if scheme == "http":
        scheme = "ws"
    elif scheme == "https":
        scheme = "wss"
    elif scheme not in {"ws", "wss"}:
        raise ValueError(
            f"invalid websocket scheme {parts.scheme!r}; expected ws://, wss://, http://, or https://"
        )
    return urlunsplit(
        (scheme, parts.netloc, parts.path, parts.query, parts.fragment)
    )


__all__ = ["PondClient"]
