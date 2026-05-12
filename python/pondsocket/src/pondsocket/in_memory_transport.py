from __future__ import annotations

import asyncio
from typing import Any

from .errors import internal
from .transport import OnCloseCallback, OnMessageHandler
from .types import Event, TransportType


class InMemoryTransport:
    __slots__ = (
        "_active",
        "_assigns",
        "_close_callbacks",
        "_id",
        "_inbox",
        "_lock",
        "_message_handler",
        "_outbox",
        "_receive_task",
    )

    def __init__(self, id: str, *, assigns: dict[str, Any] | None = None) -> None:
        self._id = id
        self._assigns: dict[str, Any] = dict(assigns or {})
        self._active = True
        self._outbox: asyncio.Queue[Event] = asyncio.Queue()
        self._inbox: asyncio.Queue[Event] = asyncio.Queue()
        self._close_callbacks: list[OnCloseCallback] = []
        self._message_handler: OnMessageHandler | None = None
        self._receive_task: asyncio.Task[None] | None = None
        self._lock = asyncio.Lock()

    def get_id(self) -> str:
        return self._id

    async def send_event(self, event: Event) -> None:
        if not self._active:
            raise internal("", "Transport is closed")
        await self._outbox.put(event)

    def get_assign(self, key: str) -> Any:
        return self._assigns.get(key)

    def set_assign(self, key: str, value: Any) -> None:
        self._assigns[key] = value

    def clone_assigns(self) -> dict[str, Any]:
        return dict(self._assigns)

    def is_active(self) -> bool:
        return self._active

    async def close(self) -> None:
        if not self._active:
            return
        self._active = False
        if self._receive_task is not None and not self._receive_task.done():
            self._receive_task.cancel()
        for cb in list(self._close_callbacks):
            try:
                await cb(self)
            except Exception:
                continue

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

    async def _receive_loop(self) -> None:
        assert self._message_handler is not None
        while True:
            try:
                ev = await self._inbox.get()
            except asyncio.CancelledError:
                return
            if not self._active:
                return
            try:
                await self._message_handler(ev, self)
            except Exception:
                continue

    def transport_type(self) -> TransportType:
        return TransportType.WEBSOCKET

    async def receive_sent(self, *, wait: float | None = 1.0) -> Event:
        if wait is None:
            return await self._outbox.get()
        return await asyncio.wait_for(self._outbox.get(), wait)

    async def push_inbound(self, event: Event) -> None:
        await self._inbox.put(event)

    def outbox_size(self) -> int:
        return self._outbox.qsize()

    async def drain_outbox(self) -> list[Event]:
        events: list[Event] = []
        while not self._outbox.empty():
            events.append(self._outbox.get_nowait())
        return events
