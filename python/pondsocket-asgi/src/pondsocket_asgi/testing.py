from __future__ import annotations

import asyncio
import json
from dataclasses import dataclass, field
from typing import Any

from pondsocket_common import uuid

from .app import PondASGIApp


@dataclass(slots=True)
class WebSocketSession:
    scope: dict[str, Any]
    inbox: asyncio.Queue[dict[str, Any]]
    outbox: asyncio.Queue[dict[str, Any]]
    app_task: asyncio.Task[None]
    accepted: bool = False
    closed: bool = False

    async def send_text(self, text: str) -> None:
        await self.inbox.put({"type": "websocket.receive", "text": text})

    async def send_json(self, data: dict[str, Any]) -> None:
        await self.send_text(json.dumps(data))

    async def send_client_message(
        self,
        *,
        action: str,
        channel_name: str,
        event: str,
        payload: dict[str, Any] | None = None,
        request_id: str | None = None,
    ) -> str:
        rid = request_id or uuid()
        await self.send_json(
            {
                "action": action,
                "channelName": channel_name,
                "event": event,
                "payload": payload or {},
                "requestId": rid,
            }
        )
        return rid

    async def receive_message(self, *, wait: float = 1.0) -> dict[str, Any]:
        msg = await asyncio.wait_for(self.outbox.get(), wait)
        return msg

    async def receive_json(self, *, wait: float = 1.0) -> dict[str, Any]:
        msg = await self.receive_message(wait=wait)
        if msg.get("type") != "websocket.send":
            raise AssertionError(f"expected websocket.send, got {msg!r}")
        text = msg.get("text")
        if text is None:
            raise AssertionError(f"expected text frame, got {msg!r}")
        data = json.loads(text)
        assert isinstance(data, dict)
        return data

    async def disconnect(self, code: int = 1000) -> None:
        if not self.closed:
            self.closed = True
            await self.inbox.put({"type": "websocket.disconnect", "code": code})
        try:
            await asyncio.wait_for(self.app_task, timeout=1.0)
        except TimeoutError:
            self.app_task.cancel()

    def is_closed(self) -> bool:
        return self.closed


@dataclass(slots=True)
class LifespanSession:
    inbox: asyncio.Queue[dict[str, Any]]
    outbox: asyncio.Queue[dict[str, Any]]
    app_task: asyncio.Task[None]

    async def startup(self) -> dict[str, Any]:
        await self.inbox.put({"type": "lifespan.startup"})
        return await asyncio.wait_for(self.outbox.get(), timeout=1.0)

    async def shutdown(self) -> dict[str, Any]:
        await self.inbox.put({"type": "lifespan.shutdown"})
        msg = await asyncio.wait_for(self.outbox.get(), timeout=1.0)
        try:
            await asyncio.wait_for(self.app_task, timeout=1.0)
        except TimeoutError:
            self.app_task.cancel()
        return msg


@dataclass(slots=True)
class _ASGIQueues:
    inbox: asyncio.Queue[dict[str, Any]] = field(default_factory=asyncio.Queue)
    outbox: asyncio.Queue[dict[str, Any]] = field(default_factory=asyncio.Queue)

    def receive(self) -> Any:
        async def _recv() -> dict[str, Any]:
            return await self.inbox.get()

        return _recv

    def send(self) -> Any:
        async def _send(message: dict[str, Any]) -> None:
            await self.outbox.put(message)

        return _send


class ASGITestDriver:
    __slots__ = ("_app",)

    def __init__(self, app: PondASGIApp) -> None:
        self._app = app

    async def connect_websocket(
        self,
        path: str,
        *,
        query_string: bytes = b"",
        headers: list[tuple[bytes, bytes]] | None = None,
        client: tuple[str, int] | None = ("127.0.0.1", 12345),
        subprotocols: list[str] | None = None,
    ) -> WebSocketSession:
        scope: dict[str, Any] = {
            "type": "websocket",
            "asgi": {"version": "3.0", "spec_version": "2.3"},
            "scheme": "ws",
            "server": ("localhost", 80),
            "client": client,
            "root_path": "",
            "path": path,
            "raw_path": path.encode("utf-8"),
            "query_string": query_string,
            "headers": headers or [],
            "subprotocols": subprotocols or [],
        }
        queues = _ASGIQueues()
        task = asyncio.create_task(self._app(scope, queues.receive(), queues.send()))
        session = WebSocketSession(
            scope=scope, inbox=queues.inbox, outbox=queues.outbox, app_task=task
        )
        await session.inbox.put({"type": "websocket.connect"})
        first = await asyncio.wait_for(session.outbox.get(), timeout=1.0)
        if first.get("type") == "websocket.accept":
            session.accepted = True
            return session
        if first.get("type") == "websocket.close":
            session.closed = True
            await asyncio.wait_for(task, timeout=1.0)
            await session.outbox.put(first)
            return session
        raise AssertionError(f"unexpected first server message: {first!r}")

    async def open_lifespan(self) -> LifespanSession:
        scope: dict[str, Any] = {
            "type": "lifespan",
            "asgi": {"version": "3.0", "spec_version": "2.0"},
        }
        queues = _ASGIQueues()
        task = asyncio.create_task(self._app(scope, queues.receive(), queues.send()))
        return LifespanSession(inbox=queues.inbox, outbox=queues.outbox, app_task=task)

    async def call_http(
        self,
        path: str,
        *,
        method: str = "GET",
        headers: list[tuple[bytes, bytes]] | None = None,
        body: bytes = b"",
    ) -> tuple[int, bytes]:
        scope: dict[str, Any] = {
            "type": "http",
            "asgi": {"version": "3.0", "spec_version": "2.3"},
            "http_version": "1.1",
            "method": method,
            "scheme": "http",
            "path": path,
            "raw_path": path.encode("utf-8"),
            "query_string": b"",
            "headers": headers or [],
            "client": ("127.0.0.1", 12345),
            "server": ("localhost", 80),
        }
        queues = _ASGIQueues()
        await queues.inbox.put(
            {"type": "http.request", "body": body, "more_body": False}
        )
        await self._app(scope, queues.receive(), queues.send())
        start = await queues.outbox.get()
        body_msg = await queues.outbox.get()
        return start["status"], body_msg.get("body", b"")

    async def connect_sse(
        self,
        path: str,
        *,
        query_string: bytes = b"",
        headers: list[tuple[bytes, bytes]] | None = None,
    ) -> SSESession:
        all_headers: list[tuple[bytes, bytes]] = [
            *(headers or []),
            (b"accept", b"text/event-stream"),
        ]
        scope: dict[str, Any] = {
            "type": "http",
            "asgi": {"version": "3.0", "spec_version": "2.3"},
            "http_version": "1.1",
            "method": "GET",
            "scheme": "http",
            "path": path,
            "raw_path": path.encode("utf-8"),
            "query_string": query_string,
            "headers": all_headers,
            "client": ("127.0.0.1", 12345),
            "server": ("localhost", 80),
        }
        queues = _ASGIQueues()
        await queues.inbox.put(
            {"type": "http.request", "body": b"", "more_body": False}
        )
        task = asyncio.create_task(
            self._app(scope, queues.receive(), queues.send())
        )
        start = await asyncio.wait_for(queues.outbox.get(), timeout=1.0)
        connection_id = ""
        accepted = False
        if start.get("type") == "http.response.start" and start.get("status") == 200:
            accepted = True
            for name, value in start.get("headers", []):
                if name == b"x-connection-id":
                    connection_id = value.decode("utf-8")
                    break
        return SSESession(
            scope=scope,
            inbox=queues.inbox,
            outbox=queues.outbox,
            app_task=task,
            connection_id=connection_id,
            status=start.get("status", 0),
            accepted=accepted,
        )

    async def sse_push(
        self, connection_id: str, payload: dict[str, Any]
    ) -> tuple[int, bytes]:
        body = json.dumps(payload).encode("utf-8")
        return await self.call_http(
            "/push",
            method="POST",
            headers=[
                (b"x-connection-id", connection_id.encode("utf-8")),
                (b"content-type", b"application/json"),
            ],
            body=body,
        )

    async def sse_disconnect(self, connection_id: str) -> tuple[int, bytes]:
        return await self.call_http(
            "/disconnect",
            method="DELETE",
            headers=[(b"x-connection-id", connection_id.encode("utf-8"))],
        )


@dataclass(slots=True)
class SSESession:
    scope: dict[str, Any]
    inbox: asyncio.Queue[dict[str, Any]]
    outbox: asyncio.Queue[dict[str, Any]]
    app_task: asyncio.Task[None]
    connection_id: str
    status: int
    accepted: bool

    async def receive_frame_bytes(self, *, wait: float = 1.0) -> bytes:
        msg = await asyncio.wait_for(self.outbox.get(), wait)
        if msg.get("type") != "http.response.body":
            raise AssertionError(f"expected http.response.body, got {msg!r}")
        body = msg.get("body", b"")
        assert isinstance(body, bytes)
        return body

    async def receive_event(self, *, wait: float = 1.0) -> dict[str, Any]:
        frame = await self.receive_frame_bytes(wait=wait)
        text = frame.decode("utf-8").strip()
        for line in text.split("\n"):
            if line.startswith("data: "):
                data = json.loads(line[6:])
                assert isinstance(data, dict)
                return data
        raise AssertionError(f"no data line in SSE frame: {text!r}")

    async def receive_event_named(
        self, event_name: str, *, wait: float = 1.0, attempts: int = 10
    ) -> dict[str, Any]:
        for _ in range(attempts):
            data = await self.receive_event(wait=wait)
            if data.get("event") == event_name:
                return data
        raise AssertionError(f"did not receive event {event_name!r}")

    async def disconnect(self) -> None:
        await self.inbox.put({"type": "http.disconnect"})
        try:
            await asyncio.wait_for(self.app_task, timeout=1.0)
        except TimeoutError:
            self.app_task.cancel()
