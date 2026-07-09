from __future__ import annotations

import asyncio
from typing import Any

import pytest

from pondsocket import (
    ConnectionContext,
    EventContext,
    InMemoryTransport,
    JoinContext,
    LeaveContext,
    OutgoingContext,
    PondSocket,
    SystemEvents,
    Transport,
)
from pondsocket.types import Event
from pondsocket_common import (
    ClientActions,
    IncomingConnection,
    PresenceEventTypes,
    ServerActions,
    uuid,
)


def _client_event(
    action: str, channel: str, event: str, payload: dict[str, Any] | None = None
) -> Event:
    return Event(
        action=action,
        channel_name=channel,
        request_id=uuid(),
        event=event,
        payload=payload if payload is not None else {},
    )


async def _next_event(t: InMemoryTransport, wait: float = 0.5) -> Event:
    return await t.receive_sent(wait=wait)


async def _expect_event(
    t: InMemoryTransport,
    action: str,
    event_name: str,
    *,
    wait: float = 0.5,
) -> Event:
    for _ in range(20):
        ev = await t.receive_sent(wait=wait)
        if ev.action == action and ev.event == event_name:
            return ev
    raise AssertionError(f"did not receive {action}/{event_name}")


async def _connect(
    pond: PondSocket,
    *,
    user_id: str | None = None,
    address: str = "127.0.0.1",
    query: dict[str, str] | None = None,
    path: str = "/api/socket",
) -> tuple[InMemoryTransport, ConnectionContext]:
    match = pond.match_endpoint(path)
    assert match is not None, f"no endpoint matches {path}"
    endpoint = match.endpoint
    incoming = IncomingConnection(
        id=user_id or uuid(),
        address=address,
        query=query or {},
    )
    ctx = await endpoint.request_connection(incoming, user_id=incoming.id)
    if not ctx.is_accepted:
        return InMemoryTransport(incoming.id), ctx
    transport = InMemoryTransport(ctx.user_id, assigns=ctx.assigns)
    await endpoint.register_transport(transport)
    return transport, ctx


async def test_connection_accept_sends_connect_event() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept(role="user")

    pond.create_endpoint("/api/socket", auth)

    transport, ctx = await _connect(pond)
    assert ctx.is_accepted
    connect_ev = await _next_event(transport)
    assert connect_ev.action == ServerActions.CONNECT.value
    assert connect_ev.event == SystemEvents.CONNECTION.value
    assert connect_ev.payload == {
        "id": transport.get_id(),
        "connectionId": transport.get_id(),
    }
    await pond.close()


async def test_connection_decline_does_not_register() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.decline(401, "Invalid token")

    endpoint = pond.create_endpoint("/api/socket", auth)
    _, ctx = await _connect(pond)
    assert ctx.is_declined
    assert ctx.decline_info == (401, "Invalid token")
    assert await endpoint.connection_count() == 0
    await pond.close()


async def test_join_channel_acknowledge_flow() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept(role="user")

    endpoint = pond.create_endpoint("/api/socket", auth)

    captured: dict[str, Any] = {}

    async def on_join(ctx: JoinContext) -> None:
        captured["room"] = ctx.route.param("room_id")
        captured["params"] = dict(ctx.join_params)
        await ctx.accept(assigns={"name": ctx.join_params.get("username")})

    endpoint.create_channel("/chat/:room_id", on_join)

    transport, _ = await _connect(pond)
    await _next_event(transport)

    join_req = _client_event(
        ClientActions.JOIN_CHANNEL.value,
        "/chat/42",
        "join",
        {"username": "daisy"},
    )
    join_req.request_id = "req-join-1"
    await transport.push_inbound(join_req)

    ack = await _expect_event(transport, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)
    assert ack.request_id == "req-join-1"
    assert ack.channel_name == "/chat/42"

    await asyncio.sleep(0.02)
    assert captured["room"] == "42"
    assert captured["params"] == {"username": "daisy"}
    await pond.close()


async def test_join_decline_sends_unauthorized() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.decline(403, "wrong room")

    endpoint.create_channel("/chat/:room_id", on_join)

    transport, _ = await _connect(pond)
    await _next_event(transport)

    join_req = _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    await transport.push_inbound(join_req)

    ev = await _expect_event(transport, ServerActions.SYSTEM.value, SystemEvents.UNAUTHORIZED.value)
    assert ev.payload == {"code": 403, "message": "wrong room"}
    await pond.close()


async def test_join_handler_without_response_auto_declines() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        return

    endpoint.create_channel("/chat/:room_id", on_join)

    transport, _ = await _connect(pond)
    await _next_event(transport)

    join_req = _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    await transport.push_inbound(join_req)

    ev = await _expect_event(transport, ServerActions.SYSTEM.value, SystemEvents.UNAUTHORIZED.value)
    assert ev.payload == {"code": 401, "message": "Join handler did not respond"}
    await pond.close()


async def test_unknown_channel_pattern_sends_not_found() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    endpoint.create_channel("/chat/:room_id", on_join)

    transport, _ = await _connect(pond)
    await _next_event(transport)

    join_req = _client_event(ClientActions.JOIN_CHANNEL.value, "/missing/path", "join")
    await transport.push_inbound(join_req)

    ev = await _expect_event(transport, ServerActions.SYSTEM.value, SystemEvents.NOT_FOUND.value)
    assert ev.payload == {"channel": "/missing/path"}
    await pond.close()


async def test_broadcast_fanouts_to_other_members() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_message(ctx: EventContext) -> None:
        await ctx.broadcast(ctx.event_name, ctx.get_payload())

    lobby.on_message("message", on_message)

    alice_t, _ = await _connect(pond, user_id="alice")
    bob_t, _ = await _connect(pond, user_id="bob")
    await _next_event(alice_t)
    await _next_event(bob_t)

    for t in (alice_t, bob_t):
        await t.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
        await _expect_event(t, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)

    await alice_t.push_inbound(
        _client_event(ClientActions.BROADCAST.value, "/chat/1", "message", {"text": "hi"})
    )

    ev_a = await _expect_event(alice_t, ServerActions.BROADCAST.value, "message")
    ev_b = await _expect_event(bob_t, ServerActions.BROADCAST.value, "message")
    assert ev_a.payload == {"text": "hi"}
    assert ev_b.payload == {"text": "hi"}
    await pond.close()


async def test_reply_uses_request_id_correlation() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_message(ctx: EventContext) -> None:
        await ctx.reply("pong", {"echo": ctx.get_payload().get("text")})

    lobby.on_message("ping", on_message)

    t, _ = await _connect(pond)
    await _next_event(t)
    await t.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(t, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)

    req = _client_event(ClientActions.BROADCAST.value, "/chat/1", "ping", {"text": "hi"})
    req.request_id = "req-ping-1"
    await t.push_inbound(req)

    reply = await _expect_event(t, ServerActions.SYSTEM.value, "pong")
    assert reply.request_id == "req-ping-1"
    assert reply.payload == {"echo": "hi"}
    await pond.close()


async def test_presence_join_then_update_delivered_to_self() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()
        await ctx.track({"status": "online"})

    endpoint.create_channel("/chat/:room_id", on_join)

    t, _ = await _connect(pond)
    await _next_event(t)
    await t.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))

    join_presence = await _expect_event(
        t, ServerActions.PRESENCE.value, PresenceEventTypes.JOIN.value
    )
    assert isinstance(join_presence.payload, dict)
    assert join_presence.payload["changed"] == {"status": "online"}
    await pond.close()


async def test_leave_channel_explicit_fires_leave_handler() -> None:
    pond = PondSocket()
    leave_events: list[tuple[str, str]] = []

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_leave(ctx: LeaveContext) -> None:
        leave_events.append((ctx.user.id, ctx.reason))

    lobby.on_leave(on_leave)

    t, _ = await _connect(pond, user_id="alice")
    await _next_event(t)
    await t.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(t, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)

    await t.push_inbound(_client_event(ClientActions.LEAVE_CHANNEL.value, "/chat/1", "leave"))

    for _ in range(40):
        if leave_events:
            break
        await asyncio.sleep(0.01)

    assert leave_events == [("alice", "explicit_leave")]
    await pond.close()


async def test_disconnect_triggers_leave_with_connection_closed_reason() -> None:
    pond = PondSocket()
    leave_events: list[tuple[str, str]] = []

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_leave(ctx: LeaveContext) -> None:
        leave_events.append((ctx.user.id, ctx.reason))

    lobby.on_leave(on_leave)

    t, _ = await _connect(pond, user_id="alice")
    await _next_event(t)
    await t.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(t, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)

    await t.close()

    for _ in range(50):
        if leave_events:
            break
        await asyncio.sleep(0.01)

    assert leave_events == [("alice", "connection_closed")]
    await pond.close()


async def test_outgoing_middleware_blocks_message() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_message(ctx: EventContext) -> None:
        await ctx.broadcast("message", ctx.get_payload())

    lobby.on_message("message", on_message)

    async def on_outgoing(ctx: OutgoingContext) -> None:
        if ctx.user.assigns.get("muted"):
            ctx.block()

    lobby.on_outgoing("message", on_outgoing)

    alice, _ = await _connect(pond, user_id="alice")
    bob, _ = await _connect(pond, user_id="bob")
    await _next_event(alice)
    await _next_event(bob)

    for t in (alice, bob):
        await t.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
        await _expect_event(t, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)

    bob.set_assign("muted", True)
    chan = await endpoint.get_transport("bob")
    assert chan is bob
    match = pond.match_endpoint("/api/socket")
    assert match is not None
    lobbies = endpoint._channel_registrations  # type: ignore[attr-defined]
    assert lobbies
    channel = await lobbies[0].lobby.get_channel("/chat/1")
    assert channel is not None
    await channel.update_assign("bob", "muted", True)

    await alice.push_inbound(
        _client_event(ClientActions.BROADCAST.value, "/chat/1", "message", {"x": 1})
    )

    ev_a = await _expect_event(alice, ServerActions.BROADCAST.value, "message")
    assert ev_a.payload == {"x": 1}
    await asyncio.sleep(0.05)
    assert bob.outbox_size() == 0
    await pond.close()


async def test_match_endpoint_returns_none_for_unknown_path() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    pond.create_endpoint("/api/socket", auth)
    assert pond.match_endpoint("/totally/different") is None
    await pond.close()


async def test_endpoint_path_with_params_is_supported() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    pond.create_endpoint("/api/:version/socket", auth)
    match = pond.match_endpoint("/api/v2/socket")
    assert match is not None
    assert match.route.params == {"version": "v2"}
    await pond.close()


async def test_transport_satisfies_protocol_at_runtime() -> None:
    t = InMemoryTransport("u1")
    assert isinstance(t, Transport)


@pytest.mark.parametrize(
    "action",
    [ClientActions.BROADCAST, ClientActions.JOIN_CHANNEL, ClientActions.LEAVE_CHANNEL],
)
async def test_client_action_enum_values_are_wire_strings(action: ClientActions) -> None:
    assert isinstance(action.value, str)
    assert action.value == action.name
