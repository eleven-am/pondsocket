from __future__ import annotations

import abc
import asyncio
from collections.abc import Callable
from typing import TypeAlias, TypeVar

from pondsocket_common import (
    BehaviorSubject,
    ChannelEvent,
    ClientMessage,
    JoinParams,
    PondSchema,
    Subject,
    Unsubscribe,
    ValidationError,
    parse_channel_event,
    to_pond_object,
)

from ._channel import Channel
from .typed import TypedChannel, typed_channel
from .types import ClientOptions, ConnectionState

ConnectionStateHandler: TypeAlias = Callable[[ConnectionState], None]
ErrorHandler: TypeAlias = Callable[[BaseException], None]
PresenceT = TypeVar("PresenceT")
AssignsT = TypeVar("AssignsT")
JoinParamsT = TypeVar("JoinParamsT")


class BaseClient(abc.ABC):
    __slots__ = (
        "_broadcaster",
        "_channels",
        "_connection_state",
        "_disconnecting",
        "_errors",
        "_options",
        "_reconnect_attempts",
        "_reconnect_task",
        "_tasks",
    )

    def __init__(self, options: ClientOptions | None = None) -> None:
        self._options = options or ClientOptions()
        self._channels: dict[str, Channel] = {}
        self._broadcaster: Subject[ChannelEvent] = Subject()
        self._connection_state: BehaviorSubject[ConnectionState] = BehaviorSubject(
            ConnectionState.DISCONNECTED
        )
        self._errors: Subject[BaseException] = Subject()
        self._reconnect_attempts: int = 0
        self._reconnect_task: asyncio.Task[None] | None = None
        self._disconnecting: bool = False
        self._tasks: set[asyncio.Task[None]] = set()
        self._broadcaster.subscribe(self._route_event)

    @property
    def options(self) -> ClientOptions:
        return self._options

    def get_state(self) -> ConnectionState:
        return self._connection_state.value or ConnectionState.DISCONNECTED

    def create_channel(
        self, name: str, params: JoinParams | None = None
    ) -> Channel:
        existing = self._channels.get(name)
        if existing is not None and existing.channel_state.value not in ("CLOSED", "DECLINED"):
            return existing
        channel = Channel(
            name=name,
            params=params,
            publisher=self._publish_outbound,
            connection_state=self._connection_state,
            max_queue_size=self._options.max_queue_size,
            response_timeout=self._options.response_timeout,
        )
        self._channels[name] = channel
        return channel

    def create_typed_channel(
        self,
        schema: PondSchema[PresenceT, AssignsT, JoinParamsT],
        name: str,
        params: JoinParamsT | None = None,
    ) -> TypedChannel[PresenceT]:
        channel = self.create_channel(name, to_pond_object(params))
        return typed_channel(channel, schema)

    def on_connection_change(self, callback: ConnectionStateHandler) -> Unsubscribe:
        return self._connection_state.subscribe(callback)

    def on_error(self, callback: ErrorHandler) -> Unsubscribe:
        return self._errors.subscribe(callback)

    async def connect(self) -> None:
        self._disconnecting = False
        if self.get_state() != ConnectionState.DISCONNECTED:
            return
        self._connection_state.publish(ConnectionState.CONNECTING)
        try:
            await self._open_transport()
        except Exception as exc:
            self._errors.publish(exc)
            self._connection_state.publish(ConnectionState.DISCONNECTED)
            self._schedule_reconnect()

    async def disconnect(self) -> None:
        self._disconnecting = True
        if self._reconnect_task is not None and not self._reconnect_task.done():
            self._reconnect_task.cancel()
            self._reconnect_task = None
        await self._close_transport()
        self._connection_state.publish(ConnectionState.DISCONNECTED)
        for channel in list(self._channels.values()):
            channel._force_close()
        self._channels.clear()

    def _mark_connected(self) -> None:
        self._reconnect_attempts = 0
        self._connection_state.publish(ConnectionState.CONNECTED)

    def _mark_disconnected(self) -> None:
        self._connection_state.publish(ConnectionState.DISCONNECTED)
        if not self._disconnecting:
            self._schedule_reconnect()

    def _handle_inbound_text(self, text: str) -> None:
        try:
            import json

            data = json.loads(text)
        except json.JSONDecodeError as e:
            self._errors.publish(e)
            return
        try:
            event = parse_channel_event(data)
        except ValidationError as e:
            self._errors.publish(e)
            return
        self._broadcaster.publish(event)

    def _route_event(self, event: ChannelEvent) -> None:
        channel = self._channels.get(event.channel_name)
        if channel is None:
            return
        channel.handle_event(event)

    def _schedule_reconnect(self) -> None:
        if self._disconnecting:
            return
        max_attempts = self._options.max_reconnect_attempts
        if max_attempts >= 0 and self._reconnect_attempts >= max_attempts:
            return
        if self._reconnect_task is not None and not self._reconnect_task.done():
            return
        delay = min(
            self._options.base_reconnect_delay * (2 ** self._reconnect_attempts),
            self._options.max_reconnect_delay,
        )
        self._reconnect_attempts += 1
        self._reconnect_task = asyncio.create_task(self._reconnect_after(delay))
        self._reconnect_task.add_done_callback(self._on_reconnect_task_done)

    def _on_reconnect_task_done(self, _task: asyncio.Task[None]) -> None:
        self._reconnect_task = None

    async def _reconnect_after(self, delay: float) -> None:
        try:
            await asyncio.sleep(delay)
        except asyncio.CancelledError:
            return
        if self._disconnecting:
            return
        await self.connect()

    async def _publish_outbound(self, message: ClientMessage) -> None:
        await self._send_outbound(message)

    @abc.abstractmethod
    async def _open_transport(self) -> None: ...

    @abc.abstractmethod
    async def _close_transport(self) -> None: ...

    @abc.abstractmethod
    async def _send_outbound(self, message: ClientMessage) -> None: ...


__all__ = [
    "BaseClient",
    "ConnectionStateHandler",
    "ErrorHandler",
]
