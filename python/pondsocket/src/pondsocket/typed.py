from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any, Generic, TypeVar, cast

from pondsocket_common import PondEvent, PondSchema, to_pond_object

from .channel import Channel
from .contexts.event_context import EventContext
from .contexts.join_context import JoinContext
from .lobby import Lobby
from .types import User

PayloadT = TypeVar("PayloadT")
ResponseT = TypeVar("ResponseT")
PresenceT = TypeVar("PresenceT")
AssignsT = TypeVar("AssignsT")
JoinParamsT = TypeVar("JoinParamsT")


class TypedChannel(Generic[PresenceT, AssignsT]):
    __slots__ = ("_raw",)

    def __init__(self, raw: Channel) -> None:
        self._raw = raw

    @property
    def raw(self) -> Channel:
        return self._raw

    @property
    def name(self) -> str:
        return self._raw.name

    async def broadcast(
        self,
        event: PondEvent[PayloadT, ResponseT],
        payload: PayloadT,
    ) -> None:
        await self._raw.broadcast(event.name, to_pond_object(payload))

    async def broadcast_to(
        self,
        event: PondEvent[PayloadT, ResponseT],
        payload: PayloadT,
        *user_ids: str,
    ) -> None:
        await self._raw.broadcast_to(event.name, to_pond_object(payload), *user_ids)

    async def broadcast_from(
        self,
        event: PondEvent[PayloadT, ResponseT],
        payload: PayloadT,
        sender_id: str,
    ) -> None:
        await self._raw.broadcast_from(event.name, to_pond_object(payload), sender_id)

    async def track_presence(self, user_id: str, presence: PresenceT) -> None:
        await self._raw.track_presence(user_id, to_pond_object(presence))

    async def update_presence(self, user_id: str, presence: PresenceT) -> None:
        await self._raw.update_presence(user_id, to_pond_object(presence))

    async def get_presence(self) -> dict[str, PresenceT]:
        return cast(dict[str, PresenceT], await self._raw.get_presence())

    async def get_assigns(self) -> dict[str, AssignsT]:
        return cast(dict[str, AssignsT], await self._raw.get_assigns())


class TypedJoinContext(Generic[PresenceT, AssignsT, JoinParamsT]):
    __slots__ = ("_raw",)

    def __init__(self, raw: JoinContext) -> None:
        self._raw = raw

    @property
    def raw(self) -> JoinContext:
        return self._raw

    @property
    def join_params(self) -> JoinParamsT:
        return cast(JoinParamsT, self._raw.join_params)

    @property
    def user_id(self) -> str:
        return self._raw.transport.get_id()

    @property
    def channel(self) -> TypedChannel[PresenceT, AssignsT]:
        return TypedChannel(self._raw.channel)

    async def accept(self, assigns: AssignsT | None = None) -> TypedJoinContext[PresenceT, AssignsT, JoinParamsT]:
        await self._raw.accept(to_pond_object(assigns))
        return self

    async def decline(self, code: int, message: str) -> None:
        await self._raw.decline(code, message)

    async def reply(
        self,
        event: PondEvent[PayloadT, ResponseT],
        payload: ResponseT,
    ) -> TypedJoinContext[PresenceT, AssignsT, JoinParamsT]:
        await self._raw.reply(event.name, to_pond_object(payload))
        return self

    async def track(self, presence: PresenceT) -> TypedJoinContext[PresenceT, AssignsT, JoinParamsT]:
        await self._raw.track(to_pond_object(presence))
        return self

    async def broadcast(
        self,
        event: PondEvent[PayloadT, ResponseT],
        payload: PayloadT,
    ) -> TypedJoinContext[PresenceT, AssignsT, JoinParamsT]:
        await self._raw.broadcast(event.name, to_pond_object(payload))
        return self

    async def assign(self, assigns: AssignsT) -> TypedJoinContext[PresenceT, AssignsT, JoinParamsT]:
        await self._raw.assign(to_pond_object(assigns))
        return self

    async def get_all_presence(self) -> dict[str, PresenceT]:
        return cast(dict[str, PresenceT], await self._raw.get_all_presence())

    async def get_all_assigns(self) -> dict[str, AssignsT]:
        return cast(dict[str, AssignsT], await self._raw.get_all_assigns())


class TypedEventContext(Generic[PayloadT, PresenceT, AssignsT]):
    __slots__ = ("_raw",)

    def __init__(self, raw: EventContext) -> None:
        self._raw = raw

    @property
    def raw(self) -> EventContext:
        return self._raw

    @property
    def payload(self) -> PayloadT:
        return cast(PayloadT, self._raw.get_payload())

    @property
    def event_name(self) -> str:
        return self._raw.event_name

    @property
    def user(self) -> User:
        return self._raw.user

    @property
    def assigns(self) -> AssignsT:
        return cast(AssignsT, self._raw.user.assigns)

    @property
    def presence(self) -> PresenceT | None:
        return cast(PresenceT | None, self._raw.user.presence)

    @property
    def channel(self) -> TypedChannel[PresenceT, AssignsT]:
        return TypedChannel(self._raw.channel)

    async def reply(
        self,
        event: PondEvent[Any, ResponseT],
        payload: ResponseT,
    ) -> TypedEventContext[PayloadT, PresenceT, AssignsT]:
        await self._raw.reply(event.name, to_pond_object(payload))
        return self

    async def broadcast(
        self,
        event: PondEvent[ResponseT, Any],
        payload: ResponseT,
    ) -> TypedEventContext[PayloadT, PresenceT, AssignsT]:
        await self._raw.broadcast(event.name, to_pond_object(payload))
        return self

    async def track(self, presence: PresenceT) -> TypedEventContext[PayloadT, PresenceT, AssignsT]:
        await self._raw.track(to_pond_object(presence))
        return self

    async def update_presence(
        self,
        presence: PresenceT,
    ) -> TypedEventContext[PayloadT, PresenceT, AssignsT]:
        await self._raw.update_presence(to_pond_object(presence))
        return self

    async def assign(self, assigns: AssignsT) -> TypedEventContext[PayloadT, PresenceT, AssignsT]:
        await self._raw.assign(to_pond_object(assigns))
        return self

    async def get_all_presence(self) -> dict[str, PresenceT]:
        return cast(dict[str, PresenceT], await self._raw.get_all_presence())

    async def get_all_assigns(self) -> dict[str, AssignsT]:
        return cast(dict[str, AssignsT], await self._raw.get_all_assigns())


TypedJoinHandler = Callable[[TypedJoinContext[PresenceT, AssignsT, JoinParamsT]], Awaitable[None]]
TypedEventHandler = Callable[[TypedEventContext[PayloadT, PresenceT, AssignsT]], Awaitable[None]]


class TypedLobby(Generic[PresenceT, AssignsT]):
    __slots__ = ("_raw",)

    def __init__(self, raw: Lobby) -> None:
        self._raw = raw

    @property
    def raw(self) -> Lobby:
        return self._raw

    def on(
        self,
        event: PondEvent[PayloadT, ResponseT],
        handler: TypedEventHandler[PayloadT, PresenceT, AssignsT],
    ) -> None:
        async def wrapped(ctx: EventContext) -> None:
            await handler(TypedEventContext(ctx))

        self._raw.on_message(event.name, wrapped)

    async def get_channel(self, name: str) -> TypedChannel[PresenceT, AssignsT] | None:
        channel = await self._raw.get_channel(name)
        if channel is None:
            return None
        return TypedChannel(channel)


def typed_lobby(
    raw: Lobby,
    _schema: PondSchema[PresenceT, AssignsT, JoinParamsT],
) -> TypedLobby[PresenceT, AssignsT]:
    return TypedLobby(raw)


__all__ = [
    "TypedChannel",
    "TypedEventContext",
    "TypedEventHandler",
    "TypedJoinContext",
    "TypedJoinHandler",
    "TypedLobby",
    "typed_lobby",
]
