from __future__ import annotations

import asyncio
from typing import Any

from pondsocket import (
    ConnectionContext,
    EventContext,
    InMemoryTransport,
    JoinContext,
    LeaveContext,
    LocalPubSub,
    PondSocket,
)
from pondsocket.types import Event
from pondsocket_common import ClientActions, IncomingConnection, ServerActions, uuid


def _cev(
    action: str, channel: str, event: str, payload: dict | None = None, rid: str | None = None
) -> Event:
    return Event(
        action=action,
        channel_name=channel,
        request_id=rid or uuid(),
        event=event,
        payload=payload if payload is not None else {},
    )


async def _make_pond(pubsub: Any = None) -> tuple[PondSocket, Any]:
    pond = PondSocket(pubsub=pubsub)

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()
        if ctx.join_params.get("track"):
            await ctx.track({"id": ctx.transport.get_id()})

    lobby = endpoint.create_channel("/chat/:room", on_join)

    async def on_leave(ctx: LeaveContext) -> None:
        await ctx.broadcast("user_left", {"id": ctx.user.id})

    lobby.on_leave(on_leave)

    async def on_say(ctx: EventContext) -> None:
        await ctx.broadcast("echo", ctx.get_payload())

    lobby.on_message("say", on_say)
    return pond, endpoint


async def _connect(endpoint: Any, uid: str) -> InMemoryTransport:
    inc = IncomingConnection(id=uid, address="127.0.0.1", query={})
    ctx = await endpoint.request_connection(inc, user_id=uid)
    transport = InMemoryTransport(uid, assigns=ctx.assigns)
    await endpoint.register_transport(transport)
    await transport.receive_sent(wait=0.5)
    return transport


async def _join(t: InMemoryTransport, room: str = "/chat/1", track: bool = False) -> None:
    await t.push_inbound(_cev(ClientActions.JOIN_CHANNEL.value, room, "join", {"track": track}))
    for _ in range(20):
        ev = await t.receive_sent(wait=0.5)
        if ev.action == ServerActions.SYSTEM.value and ev.event == "ACKNOWLEDGE":
            return
    raise AssertionError("no ack")


async def _drain(t: InMemoryTransport, wait: float = 0.2) -> list[tuple[str, str]]:
    out: list[tuple[str, str]] = []
    while True:
        try:
            ev = await t.receive_sent(wait=wait)
        except Exception:
            break
        out.append((ev.action, ev.event))
    return out


async def test_explicit_leave_acks_and_notifies_others() -> None:
    pond, endpoint = await _make_pond()
    a = await _connect(endpoint, "alice")
    b = await _connect(endpoint, "bob")
    await _join(a, track=True)
    await _join(b, track=True)
    await asyncio.sleep(0.05)
    await _drain(a)
    await _drain(b)
    await a.push_inbound(_cev(ClientActions.LEAVE_CHANNEL.value, "/chat/1", "leave"))
    await asyncio.sleep(0.1)
    assert await _drain(a) == [("SYSTEM", "EXIT_ACKNOWLEDGE")]
    b_events = await _drain(b)
    assert ("PRESENCE", "LEAVE") in b_events
    assert ("BROADCAST", "user_left") in b_events
    await pond.close()


async def test_disconnect_notifies_others_like_leave() -> None:
    pond, endpoint = await _make_pond()
    a = await _connect(endpoint, "alice")
    b = await _connect(endpoint, "bob")
    await _join(a, track=True)
    await _join(b, track=True)
    await asyncio.sleep(0.05)
    await _drain(a)
    await _drain(b)
    await a.close()
    await asyncio.sleep(0.1)
    b_events = await _drain(b)
    assert ("PRESENCE", "LEAVE") in b_events
    assert ("BROADCAST", "user_left") in b_events
    await pond.close()


async def test_leave_of_never_joined_channel_is_silent() -> None:
    pond, endpoint = await _make_pond()
    c = await _connect(endpoint, "carol")
    await c.push_inbound(_cev(ClientActions.LEAVE_CHANNEL.value, "/nope/9", "leave"))
    await asyncio.sleep(0.1)
    assert await _drain(c) == []
    await pond.close()


async def test_non_member_broadcast_to_live_channel_is_silently_dropped() -> None:
    pond, endpoint = await _make_pond()
    a = await _connect(endpoint, "alice")
    await _join(a)
    await asyncio.sleep(0.05)
    await _drain(a)
    dave = await _connect(endpoint, "dave")
    await dave.push_inbound(
        _cev(ClientActions.BROADCAST.value, "/chat/1", "say", {"text": "intrude"})
    )
    await asyncio.sleep(0.1)
    assert await _drain(dave) == []
    assert await _drain(a) == []
    await pond.close()


async def test_broadcast_to_nonexistent_channel_returns_not_found() -> None:
    pond, endpoint = await _make_pond()
    dave = await _connect(endpoint, "dave")
    await dave.push_inbound(_cev(ClientActions.BROADCAST.value, "/ghost/1", "say", {"text": "x"}))
    await asyncio.sleep(0.1)
    assert ("SYSTEM", "NOT_FOUND") in await _drain(dave)
    await pond.close()


async def test_double_leave_while_populated_sends_one_exit_acknowledge() -> None:
    import json

    published_types: list[str] = []

    class RecordingPubSub(LocalPubSub):
        async def publish(self, topic: str, data: bytes) -> None:
            try:
                published_types.append(str(json.loads(data.decode("utf-8")).get("type")))
            except Exception:
                published_types.append("")
            await super().publish(topic, data)

    pond, endpoint = await _make_pond(RecordingPubSub())
    a = await _connect(endpoint, "alice")
    b = await _connect(endpoint, "bob")
    await _join(a)
    await _join(b)
    await asyncio.sleep(0.05)
    await _drain(a)
    await _drain(b)
    published_types.clear()
    await a.push_inbound(_cev(ClientActions.LEAVE_CHANNEL.value, "/chat/1", "leave", rid="l1"))
    await a.push_inbound(_cev(ClientActions.LEAVE_CHANNEL.value, "/chat/1", "leave", rid="l2"))
    await asyncio.sleep(0.1)
    acks = [e for e in await _drain(a) if e == ("SYSTEM", "EXIT_ACKNOWLEDGE")]
    assert len(acks) == 1
    assert "USER_REMOVE" not in published_types
    await pond.close()
