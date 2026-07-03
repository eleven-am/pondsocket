from __future__ import annotations

from collections.abc import Callable
from typing import Generic, TypeVar, cast

from pondsocket_common import (
    ChannelState,
    PondEvent,
    PondSchema,
    PresencePayloadModel,
    ServerMessage,
    TypedPresencePayload,
    Unsubscribe,
    from_pond_object,
    to_pond_object,
)

from ._channel import Channel, ChannelStateHandler, MessageHandler, ResponseTimeoutError

PayloadT = TypeVar("PayloadT")
ResponseT = TypeVar("ResponseT")
PresenceT = TypeVar("PresenceT")
AssignsT = TypeVar("AssignsT")
JoinParamsT = TypeVar("JoinParamsT")


class TypedChannel(Generic[PresenceT]):
    __slots__ = ("_presence_type", "_raw")

    def __init__(self, raw: Channel, presence_type: type[PresenceT] | None = None) -> None:
        self._raw = raw
        self._presence_type = presence_type

    @property
    def raw(self) -> Channel:
        return self._raw

    @property
    def name(self) -> str:
        return self._raw.name

    @property
    def channel_state(self) -> ChannelState:
        return self._raw.channel_state

    @property
    def presence(self) -> list[PresenceT]:
        return [self._to_presence(p) for p in self._raw.presence]

    def join(self) -> None:
        self._raw.join()

    def leave(self) -> None:
        self._raw.leave()

    def send(self, event: PondEvent[PayloadT, ResponseT], payload: PayloadT) -> None:
        self._raw.send_message(event.name, to_pond_object(payload))

    async def request(
        self,
        event: PondEvent[PayloadT, ResponseT],
        payload: PayloadT,
        *,
        wait: float | None = None,
    ) -> ResponseT:
        response = await self._raw.send_for_response(
            event.name,
            to_pond_object(payload),
            wait=wait,
        )
        return cast(ResponseT, from_pond_object(response, event.response_type))

    def on(
        self,
        event: PondEvent[PayloadT, ResponseT],
        callback: Callable[[PayloadT], None],
    ) -> Unsubscribe:
        def wrapped(message: ServerMessage) -> None:
            callback(cast(PayloadT, from_pond_object(message.payload, event.payload_type)))

        return self._raw.on_message_event(event.name, wrapped)

    def on_message(self, callback: MessageHandler) -> Unsubscribe:
        return self._raw.on_message(callback)

    def on_channel_state_change(self, callback: ChannelStateHandler) -> Unsubscribe:
        return self._raw.on_channel_state_change(callback)

    def on_join(self, callback: Callable[[PresenceT], None]) -> Unsubscribe:
        return self._raw.on_join(lambda presence: callback(self._to_presence(presence)))

    def on_leave(self, callback: Callable[[PresenceT], None]) -> Unsubscribe:
        return self._raw.on_leave(lambda presence: callback(self._to_presence(presence)))

    def on_presence_change(
        self,
        callback: Callable[[TypedPresencePayload[PresenceT]], None],
    ) -> Unsubscribe:
        def wrapped(payload: PresencePayloadModel) -> None:
            callback(
                TypedPresencePayload(
                    changed=self._to_presence(payload.changed),
                    presence=[self._to_presence(p) for p in payload.presence],
                )
            )

        return self._raw.on_presence_change(wrapped)

    def on_users_change(self, callback: Callable[[list[PresenceT]], None]) -> Unsubscribe:
        return self._raw.on_users_change(
            lambda users: callback([self._to_presence(u) for u in users])
        )

    def _to_presence(self, value: object) -> PresenceT:
        return cast(PresenceT, from_pond_object(value, self._presence_type))


def typed_channel(
    raw: Channel,
    schema: PondSchema[PresenceT, AssignsT, JoinParamsT],
) -> TypedChannel[PresenceT]:
    return TypedChannel(raw, schema.presence_type)


__all__ = [
    "ResponseTimeoutError",
    "TypedChannel",
    "typed_channel",
]
