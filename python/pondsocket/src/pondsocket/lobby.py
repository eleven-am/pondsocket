from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import TypeAlias

from .channel import Channel, ChannelOptions, LeaveMiddlewareFn
from .contexts.event_context import EventContext
from .contexts.leave_context import LeaveContext
from .contexts.outgoing_context import OutgoingContext
from .errors import PondError
from .heartbeat import HeartbeatCoordinator
from .middleware import Middleware, execute_with_middleware
from .parser import parse
from .pubsub import PubSub
from .types import MessageEvent, Options

EventHandler: TypeAlias = Callable[[EventContext], Awaitable[None]]
LeaveHandler: TypeAlias = Callable[[LeaveContext], Awaitable[None]]
OutgoingHandler: TypeAlias = Callable[[OutgoingContext], Awaitable[None]]

EventMiddlewareFn: TypeAlias = Callable[
    [EventContext, Callable[[], Awaitable[None]]], Awaitable[None]
]
OutgoingMiddlewareFn: TypeAlias = Callable[
    [OutgoingContext, Callable[[], Awaitable[None]]], Awaitable[None]
]


class Lobby:
    __slots__ = (
        "_channels",
        "_creation_tasks",
        "_endpoint_path",
        "_heartbeat",
        "_leave_handler",
        "_leave_middlewares",
        "_lock",
        "_message_middleware",
        "_options",
        "_outgoing_middleware",
        "_path",
        "_pubsub",
    )

    def __init__(
        self,
        *,
        path: str,
        endpoint_path: str = "",
        options: Options | None = None,
        pubsub: PubSub | None = None,
        heartbeat: HeartbeatCoordinator | None = None,
    ) -> None:
        self._path = path
        self._endpoint_path = endpoint_path
        self._options = options or Options()
        self._pubsub = pubsub
        self._heartbeat = heartbeat
        self._channels: dict[str, Channel] = {}
        self._creation_tasks: dict[str, asyncio.Task[Channel]] = {}
        self._message_middleware: Middleware[MessageEvent, Channel] = Middleware()
        self._outgoing_middleware: Middleware[OutgoingContext, None] = Middleware()
        self._leave_handler: LeaveHandler | None = None
        self._leave_middlewares: list[LeaveMiddlewareFn] = []
        self._lock = asyncio.Lock()

    @property
    def path(self) -> str:
        return self._path

    @property
    def options(self) -> Options:
        return self._options

    def on_message(
        self,
        event_pattern: str,
        handler: EventHandler,
        *middlewares: EventMiddlewareFn,
    ) -> None:
        chain = list(middlewares)

        async def wrapper(
            req: MessageEvent,
            resp: Channel,
            nxt: Callable[[], Awaitable[None]],
        ) -> None:
            try:
                route = parse(event_pattern, req.event.event)
            except PondError:
                await nxt()
                return
            ctx = EventContext(channel=resp, message=req, route=route)
            await execute_with_middleware(ctx, handler, chain)

        self._message_middleware.use_sync(wrapper)

    def on_leave(
        self,
        handler: LeaveHandler,
        *middlewares: LeaveMiddlewareFn,
    ) -> None:
        self._leave_handler = handler
        self._leave_middlewares = list(middlewares)

    def on_outgoing(
        self,
        event_pattern: str,
        handler: OutgoingHandler,
        *middlewares: OutgoingMiddlewareFn,
    ) -> None:
        chain = list(middlewares)

        async def wrapper(
            req: OutgoingContext,
            _resp: None,
            nxt: Callable[[], Awaitable[None]],
        ) -> None:
            try:
                route = parse(event_pattern, req.event.event)
            except PondError:
                await nxt()
                return
            req.route = route
            await execute_with_middleware(req, handler, chain)

        self._outgoing_middleware.use_sync(wrapper)

    async def has_channel(self, name: str) -> bool:
        async with self._lock:
            return name in self._channels

    async def get_channel(self, name: str) -> Channel | None:
        async with self._lock:
            return self._channels.get(name)

    async def get_or_create_channel(self, name: str) -> Channel:
        async with self._lock:
            existing = self._channels.get(name)
            if existing is not None:
                return existing
            creation_task = self._creation_tasks.get(name)
            if creation_task is None:
                creation_task = asyncio.create_task(
                    self._create_channel(name),
                    name=f"pondsocket-channel-create:{name}",
                )
                creation_task.add_done_callback(self._consume_creation_result)
                self._creation_tasks[name] = creation_task
        return await asyncio.shield(creation_task)

    async def _create_channel(self, name: str) -> Channel:
        candidate = self._build_channel(name)
        current_task = asyncio.current_task()
        try:
            await candidate.start()
            async with self._lock:
                if self._creation_tasks.get(name) is not current_task:
                    raise asyncio.CancelledError
                self._channels[name] = candidate
                self._creation_tasks.pop(name, None)
            return candidate
        except BaseException:
            await candidate.close()
            async with self._lock:
                if self._creation_tasks.get(name) is current_task:
                    self._creation_tasks.pop(name, None)
            raise

    @staticmethod
    def _consume_creation_result(task: asyncio.Task[Channel]) -> None:
        if not task.cancelled():
            task.exception()

    def _build_channel(self, name: str) -> Channel:
        async def on_destroy() -> None:
            async with self._lock:
                self._channels.pop(name, None)

        opts = ChannelOptions(
            name=name,
            options=self._options,
            message_middleware=self._message_middleware,
            outgoing_middleware=self._outgoing_middleware,
            leave_handler=self._leave_handler,
            leave_middlewares=self._leave_middlewares,
            on_destroy=on_destroy,
            endpoint_path=self._endpoint_path,
            pubsub=self._pubsub,
            heartbeat=self._heartbeat,
        )
        return Channel(opts)

    async def list_channels(self) -> list[Channel]:
        async with self._lock:
            return list(self._channels.values())

    async def close(self) -> None:
        async with self._lock:
            channels = list(self._channels.values())
            self._channels.clear()
            creation_tasks = list(self._creation_tasks.values())
            self._creation_tasks.clear()
        for task in creation_tasks:
            task.cancel()
        if creation_tasks:
            await asyncio.gather(*creation_tasks, return_exceptions=True)
        for ch in channels:
            await ch.close()
