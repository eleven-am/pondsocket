from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import Any, TypeAlias

from pondsocket.errors import internal
from pondsocket.transport import OnCloseCallback, OnMessageHandler
from pondsocket.types import Event, SystemEntity, TransportType
from pondsocket_common import ErrorTypes, ServerActions, ValidationError, uuid

from ._serialize import event_to_json, parse_inbound_text

ASGISend: TypeAlias = Callable[[dict[str, Any]], Awaitable[None]]
ASGIReceive: TypeAlias = Callable[[], Awaitable[dict[str, Any]]]


class ASGIWebSocketTransport:
    __slots__ = (
        "_active",
        "_assigns",
        "_close_callbacks",
        "_closed_event",
        "_id",
        "_message_handler",
        "_receive",
        "_receive_task",
        "_send",
        "_send_lock",
    )

    def __init__(
        self,
        *,
        id: str,
        send: ASGISend,
        receive: ASGIReceive,
        assigns: dict[str, Any] | None = None,
    ) -> None:
        self._id = id
        self._send = send
        self._receive = receive
        self._assigns: dict[str, Any] = dict(assigns or {})
        self._active = True
        self._close_callbacks: list[OnCloseCallback] = []
        self._message_handler: OnMessageHandler | None = None
        self._receive_task: asyncio.Task[None] | None = None
        self._closed_event = asyncio.Event()
        self._send_lock = asyncio.Lock()

    def get_id(self) -> str:
        return self._id

    def get_assign(self, key: str) -> Any:
        return self._assigns.get(key)

    def set_assign(self, key: str, value: Any) -> None:
        self._assigns[key] = value

    def clone_assigns(self) -> dict[str, Any]:
        return dict(self._assigns)

    def is_active(self) -> bool:
        return self._active

    def transport_type(self) -> TransportType:
        return TransportType.WEBSOCKET

    async def send_event(self, event: Event) -> None:
        if not self._active:
            raise internal("", "Transport is closed")
        wire = event_to_json(event)
        async with self._send_lock:
            try:
                await self._send({"type": "websocket.send", "text": wire})
            except Exception as e:
                raise internal("", f"WebSocket send failed: {e}") from e

    async def close(self, *, code: int = 1000, reason: str = "") -> None:
        if not self._active:
            return
        self._active = False
        try:
            async with self._send_lock:
                await self._send(
                    {"type": "websocket.close", "code": code, "reason": reason[:123]}
                )
        except Exception:
            pass
        await self._fire_close_callbacks()
        if self._receive_task is not None and not self._receive_task.done():
            self._receive_task.cancel()
        self._closed_event.set()

    def on_close(self, callback: OnCloseCallback) -> None:
        self._close_callbacks.append(callback)

    def on_message(self, handler: OnMessageHandler) -> None:
        self._message_handler = handler

    async def handle_messages(self) -> None:
        if self._message_handler is None:
            raise RuntimeError("on_message must be set before handle_messages")
        if self._receive_task is not None and not self._receive_task.done():
            return
        self._receive_task = asyncio.create_task(self._receive_loop())

    async def wait_until_closed(self) -> None:
        await self._closed_event.wait()

    async def _receive_loop(self) -> None:
        assert self._message_handler is not None
        try:
            while True:
                try:
                    msg = await self._receive()
                except asyncio.CancelledError:
                    return
                if not self._active:
                    return
                msg_type = msg.get("type")
                if msg_type == "websocket.disconnect":
                    self._active = False
                    await self._fire_close_callbacks()
                    self._closed_event.set()
                    return
                if msg_type != "websocket.receive":
                    continue
                await self._dispatch_receive(msg)
        except asyncio.CancelledError:
            return

    async def _dispatch_receive(self, msg: dict[str, Any]) -> None:
        text = msg.get("text")
        if text is None:
            bytes_payload = msg.get("bytes")
            if bytes_payload is None:
                return
            try:
                text = bytes_payload.decode("utf-8")
            except UnicodeDecodeError:
                await self._send_invalid_message("Binary frame is not valid UTF-8")
                return
        try:
            event = parse_inbound_text(text)
        except ValidationError as e:
            await self._send_invalid_message(str(e))
            return
        if self._message_handler is None:
            return
        try:
            await self._message_handler(event, self)
        except Exception:
            return

    async def _fire_close_callbacks(self) -> None:
        for cb in list(self._close_callbacks):
            try:
                await cb(self)
            except Exception:
                continue

    async def _send_invalid_message(self, detail: str) -> None:
        ev = Event(
            action=ServerActions.ERROR.value,
            channel_name=SystemEntity.GATEWAY.value,
            request_id=uuid(),
            event=ErrorTypes.INVALID_MESSAGE.value,
            payload={"error": detail},
        )
        try:
            await self.send_event(ev)
        except Exception:
            return
