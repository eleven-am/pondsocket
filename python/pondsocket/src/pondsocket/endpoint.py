from __future__ import annotations

import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TypeAlias

from pondsocket_common import ClientActions, IncomingConnection, ServerActions, uuid

from .channel import Channel
from .contexts.connection_context import ConnectionContext, ConnectionDecision
from .contexts.join_context import JoinContext
from .errors import PondError, forbidden, not_found
from .lobby import Lobby
from .middleware import execute_with_middleware
from .parser import parse
from .pubsub import PubSub
from .store import Store
from .transport import Transport
from .types import (
    Event,
    LeaveReason,
    Options,
    SystemEntity,
    SystemEvents,
    User,
)

ConnectionHandler: TypeAlias = Callable[[ConnectionContext], Awaitable[None]]
ConnectionMiddlewareFn: TypeAlias = Callable[
    [ConnectionContext, Callable[[], Awaitable[None]]], Awaitable[None]
]
JoinHandler: TypeAlias = Callable[[JoinContext], Awaitable[None]]
JoinMiddlewareFn: TypeAlias = Callable[
    [JoinContext, Callable[[], Awaitable[None]]], Awaitable[None]
]


@dataclass(slots=True)
class _ChannelRegistration:
    pattern: str
    handler: JoinHandler
    middlewares: list[JoinMiddlewareFn]
    lobby: Lobby


class Endpoint:
    __slots__ = (
        "_channel_registrations",
        "_connect_times",
        "_connection_handler",
        "_connection_middlewares",
        "_connections",
        "_options",
        "_path",
        "_pubsub",
    )

    def __init__(
        self,
        *,
        path: str,
        connection_handler: ConnectionHandler,
        options: Options | None = None,
        pubsub: PubSub | None = None,
        connection_middlewares: list[ConnectionMiddlewareFn] | None = None,
    ) -> None:
        self._path = path
        self._connection_handler = connection_handler
        self._connection_middlewares = list(connection_middlewares or [])
        self._options = options or Options()
        self._pubsub = pubsub
        self._connections: Store[Transport] = Store()
        self._channel_registrations: list[_ChannelRegistration] = []
        self._connect_times: dict[str, float] = {}

    @property
    def path(self) -> str:
        return self._path

    def use(self, *middlewares: ConnectionMiddlewareFn) -> None:
        self._connection_middlewares.extend(middlewares)

    def create_channel(
        self,
        pattern: str,
        handler: JoinHandler,
        *middlewares: JoinMiddlewareFn,
    ) -> Lobby:
        lobby = Lobby(
            path=pattern,
            endpoint_path=self._path,
            options=self._options,
            pubsub=self._pubsub,
        )
        self._channel_registrations.append(
            _ChannelRegistration(
                pattern=pattern,
                handler=handler,
                middlewares=list(middlewares),
                lobby=lobby,
            )
        )
        return lobby

    async def request_connection(
        self,
        incoming: IncomingConnection,
        *,
        user_id: str | None = None,
    ) -> ConnectionContext:
        uid = user_id or uuid()
        ctx = ConnectionContext(user_id=uid, request=incoming)
        try:
            await execute_with_middleware(
                ctx, self._connection_handler, self._connection_middlewares
            )
        except PondError as e:
            if ctx.decision is ConnectionDecision.PENDING:
                ctx.decline(e.code, e.message)
        except Exception as e:
            if ctx.decision is ConnectionDecision.PENDING:
                ctx.decline(500, str(e))
        if ctx.decision is ConnectionDecision.PENDING:
            ctx.decline(401, "Connection handler did not accept or decline")
        return ctx

    async def register_transport(self, transport: Transport) -> None:
        await self._check_max_connections()
        await self._connections.create(transport.get_id(), transport)
        self._connect_times[transport.get_id()] = time.monotonic()
        await self._send_connect_event(transport)
        transport.on_close(self._on_transport_closed)
        transport.on_message(self._handle_transport_message)
        self._fire_metric_connection_opened(transport)
        await self._fire_on_connect(transport)
        await transport.handle_messages()

    async def _check_max_connections(self) -> None:
        if self._options.max_connections <= 0:
            return
        current = await self._connections.len()
        if current >= self._options.max_connections:
            raise forbidden(
                SystemEntity.GATEWAY.value,
                "Maximum connections reached",
            )

    async def _send_connect_event(self, transport: Transport) -> None:
        transport_id = transport.get_id()
        ev = Event(
            action=ServerActions.CONNECT.value,
            channel_name=SystemEntity.GATEWAY.value,
            request_id=uuid(),
            event=SystemEvents.CONNECTION.value,
            payload={"id": transport_id, "connectionId": transport_id},
        )
        try:
            await transport.send_event(ev)
        except Exception:
            return

    async def _on_transport_closed(self, transport: Transport) -> None:
        user_id = transport.get_id()
        for reg in list(self._channel_registrations):
            for channel in await reg.lobby.list_channels():
                if await channel.has_user(user_id):
                    await channel.remove_user(
                        user_id, reason=LeaveReason.CONNECTION_CLOSED.value
                    )
        await self._connections.discard(user_id)
        connect_at = self._connect_times.pop(user_id, None)
        duration = (
            time.monotonic() - connect_at if connect_at is not None else 0.0
        )
        self._fire_metric_connection_closed(user_id, duration)
        await self._fire_on_disconnect(transport)

    async def _handle_transport_message(self, event: Event, transport: Transport) -> None:
        action = event.action
        if action == ClientActions.JOIN_CHANNEL.value:
            await self._handle_join(event, transport)
        elif action == ClientActions.LEAVE_CHANNEL.value:
            await self._handle_leave(event, transport)
        elif action == ClientActions.BROADCAST.value:
            await self._handle_broadcast(event, transport)

    async def _handle_join(self, event: Event, transport: Transport) -> None:
        channel_name = event.channel_name
        for reg in self._channel_registrations:
            try:
                route = parse(reg.pattern, channel_name)
            except PondError:
                continue
            channel = await reg.lobby.get_or_create_channel(channel_name)
            ctx = JoinContext(
                channel=channel,
                event=event,
                transport=transport,
                route=route,
            )
            await self._fire_before_join(transport, channel_name)
            try:
                await execute_with_middleware(ctx, reg.handler, reg.middlewares)
            except PondError as e:
                if not ctx.has_responded:
                    await ctx.decline(e.code, e.message)
            except Exception as e:
                if not ctx.has_responded:
                    await ctx.decline(500, str(e))
            if not ctx.has_responded:
                await ctx.decline(401, "Join handler did not respond")
            if ctx.is_accepted:
                await self._fire_after_join(transport, channel_name)
            return
        await self._send_not_found(transport, event)

    async def _handle_leave(self, event: Event, transport: Transport) -> None:
        channel = await self._find_channel(event.channel_name)
        if channel is None:
            return
        await channel.remove_user(
            transport.get_id(), reason=LeaveReason.EXPLICIT_LEAVE.value
        )
        ack = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=event.channel_name,
            request_id=event.request_id,
            event=SystemEvents.EXIT_ACKNOWLEDGE.value,
            payload={},
        )
        try:
            await transport.send_event(ack)
        except Exception:
            return

    async def _handle_broadcast(self, event: Event, transport: Transport) -> None:
        channel = await self._find_channel(event.channel_name)
        if channel is None:
            await self._send_not_found(transport, event)
            return
        await channel.handle_incoming_event(event, transport.get_id())

    async def _find_channel(self, name: str) -> Channel | None:
        for reg in self._channel_registrations:
            ch = await reg.lobby.get_channel(name)
            if ch is not None:
                return ch
        return None

    async def _send_not_found(self, transport: Transport, event: Event) -> None:
        ev = Event(
            action=ServerActions.SYSTEM.value,
            channel_name=event.channel_name,
            request_id=event.request_id,
            event=SystemEvents.NOT_FOUND.value,
            payload={"channel": event.channel_name},
        )
        try:
            await transport.send_event(ev)
        except Exception:
            return

    async def close_connection(self, user_id: str) -> None:
        transport = await self._connections.get(user_id)
        if transport is None:
            raise not_found(self._path, f"Connection {user_id} not found")
        await transport.close()

    async def get_transport(self, user_id: str) -> Transport | None:
        return await self._connections.get(user_id)

    async def connection_count(self) -> int:
        return await self._connections.len()

    async def close(self) -> None:
        for reg in list(self._channel_registrations):
            await reg.lobby.close()
        for transport in await self._connections.values():
            await transport.close()
        await self._connections.clear()
        self._connect_times.clear()

    def _fire_metric_connection_opened(self, transport: Transport) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.metrics is None:
            return
        try:
            hooks.metrics.connection_opened(transport.get_id(), self._path)
        except Exception:
            return

    def _fire_metric_connection_closed(self, user_id: str, duration: float) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.metrics is None:
            return
        try:
            hooks.metrics.connection_closed(user_id, duration)
        except Exception:
            return

    async def _fire_on_connect(self, transport: Transport) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.on_connect is None:
            return
        try:
            await hooks.on_connect(transport)
        except Exception:
            return

    async def _fire_on_disconnect(self, transport: Transport) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.on_disconnect is None:
            return
        try:
            await hooks.on_disconnect(transport)
        except Exception:
            return

    async def _fire_before_join(
        self, transport: Transport, channel_name: str
    ) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.before_join is None:
            return
        user = User(
            id=transport.get_id(),
            assigns=dict(transport.clone_assigns()),
        )
        try:
            await hooks.before_join(user, channel_name)
        except Exception:
            return

    async def _fire_after_join(
        self, transport: Transport, channel_name: str
    ) -> None:
        hooks = self._options.hooks
        if hooks is None or hooks.after_join is None:
            return
        user = User(
            id=transport.get_id(),
            assigns=dict(transport.clone_assigns()),
        )
        try:
            await hooks.after_join(user, channel_name)
        except Exception:
            return
