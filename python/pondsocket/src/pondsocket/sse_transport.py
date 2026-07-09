from __future__ import annotations

import asyncio
from typing import Any

from pondsocket_common import ErrorTypes, ServerActions, uuid

from .errors import internal
from .transport import OnCloseCallback, OnMessageHandler
from .types import Event, SystemEntity, TransportType


class SSEServerTransport:
    __slots__ = (
        "_active",
        "_assigns",
        "_close_callbacks",
        "_closed_event",
        "_id",
        "_message_handler",
        "_outbound",
    )

    def __init__(
        self,
        *,
        id: str,
        assigns: dict[str, Any] | None = None,
    ) -> None:
        self._id = id
        self._assigns: dict[str, Any] = dict(assigns or {})
        self._active = True
        self._outbound: asyncio.Queue[Event] = asyncio.Queue()
        self._close_callbacks: list[OnCloseCallback] = []
        self._message_handler: OnMessageHandler | None = None
        self._closed_event = asyncio.Event()

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
        return TransportType.SSE

    async def send_event(self, event: Event) -> None:
        if not self._active:
            raise internal("", "Transport is closed")
        await self._outbound.put(event)

    def on_close(self, callback: OnCloseCallback) -> None:
        self._close_callbacks.append(callback)

    def on_message(self, handler: OnMessageHandler) -> None:
        self._message_handler = handler

    async def handle_messages(self) -> None:
        return

    async def push_message(self, data: bytes) -> None:
        if not self._active or self._message_handler is None:
            return
        from pondsocket_common import ValidationError

        from .wire import parse_inbound_text

        try:
            text = data.decode("utf-8")
        except UnicodeDecodeError:
            await self._send_invalid_message("Binary frame is not valid UTF-8")
            return
        try:
            event = parse_inbound_text(text)
        except (ValidationError, RecursionError) as e:
            await self._send_invalid_message(str(e))
            return
        try:
            await self._message_handler(event, self)
        except Exception:
            return

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

    async def close(self) -> None:
        if not self._active:
            return
        self._active = False
        self._closed_event.set()
        for cb in list(self._close_callbacks):
            try:
                await cb(self)
            except Exception:
                continue

    async def next_event(self) -> Event | None:
        if not self._active and self._outbound.empty():
            return None
        get_task = asyncio.create_task(self._outbound.get())
        close_task = asyncio.create_task(self._closed_event.wait())
        try:
            done, pending = await asyncio.wait(
                {get_task, close_task},
                return_when=asyncio.FIRST_COMPLETED,
            )
        except asyncio.CancelledError:
            get_task.cancel()
            close_task.cancel()
            raise
        for t in pending:
            t.cancel()
        if get_task in done:
            return get_task.result()
        if not self._outbound.empty():
            try:
                return self._outbound.get_nowait()
            except asyncio.QueueEmpty:
                return None
        return None

    async def wait_until_closed(self) -> None:
        await self._closed_event.wait()
