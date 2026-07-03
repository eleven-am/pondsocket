from __future__ import annotations

from typing import TypedDict, assert_type, cast

from pondsocket_client import BaseClient, TypedChannel

from pondsocket import TypedEventContext, TypedJoinContext
from pondsocket_common import (
    PondEvent,
    PondSchema,
    TypedPresencePayload,
    pond_event,
    pond_schema,
)


class ChatPayload(TypedDict):
    text: str


class PingPayload(TypedDict):
    n: int


class PongPayload(TypedDict):
    ok: bool


class Presence(TypedDict):
    user_id: str
    status: str


class Assigns(TypedDict):
    role: str


class JoinParams(TypedDict):
    token: str


chat: PondEvent[ChatPayload, ChatPayload] = pond_event("chat")
ping: PondEvent[PingPayload, PongPayload] = pond_event("ping")
schema: PondSchema[Presence, Assigns, JoinParams] = pond_schema("chat-room")

client = cast(BaseClient, object())
join_params: JoinParams = {"token": "secret"}
chat_payload: ChatPayload = {"text": "hello"}
ping_payload: PingPayload = {"n": 1}
client_channel = client.create_typed_channel(schema, "room:1", join_params)
assert_type(client_channel, TypedChannel[Presence])

client_channel.send(chat, chat_payload)


def on_presence(payload: TypedPresencePayload[Presence]) -> None:
    assert_type(payload.changed, Presence)
    assert_type(payload.presence, list[Presence])


client_channel.on_presence_change(on_presence)


async def client_request() -> None:
    response = await client_channel.request(ping, ping_payload)
    assert_type(response, PongPayload)


async def join_handler(ctx: TypedJoinContext[Presence, Assigns, JoinParams]) -> None:
    assert_type(ctx.join_params, JoinParams)
    await ctx.accept({"role": "admin"})
    await ctx.track({"user_id": "u1", "status": "online"})
    presence = await ctx.get_all_presence()
    assert_type(presence, dict[str, Presence])


async def message_handler(ctx: TypedEventContext[ChatPayload, Presence, Assigns]) -> None:
    assert_type(ctx.payload, ChatPayload)
    assert_type(ctx.assigns, Assigns)
    assert_type(ctx.presence, Presence | None)
    reply_payload: ChatPayload = {"text": "ok"}
    await ctx.reply(chat, reply_payload)
