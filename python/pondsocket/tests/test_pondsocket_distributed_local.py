from __future__ import annotations

import asyncio

from pondsocket import (
    ConnectionContext,
    EventContext,
    JoinContext,
    LocalPubSub,
    PondSocket,
    SystemEvents,
)
from pondsocket.in_memory_transport import InMemoryTransport
from pondsocket.types import Event
from pondsocket_common import (
    ClientActions,
    PresenceEventTypes,
    ServerActions,
    uuid,
)


def _client_event(action: str, channel: str, event: str, payload: dict | None = None) -> Event:
    return Event(
        action=action,
        channel_name=channel,
        request_id=uuid(),
        event=event,
        payload=payload or {},
    )


async def _build_pond_pair(
    bus: LocalPubSub,
) -> tuple[PondSocket, PondSocket]:
    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    async def on_message(ctx: EventContext) -> None:
        await ctx.broadcast(ctx.event_name, ctx.get_payload())

    pond_a = PondSocket(pubsub=bus)
    endpoint_a = pond_a.create_endpoint("/api/socket", auth)
    lobby_a = endpoint_a.create_channel("/chat/:room_id", on_join)
    lobby_a.on_message("message", on_message)

    pond_b = PondSocket(pubsub=bus)
    endpoint_b = pond_b.create_endpoint("/api/socket", auth)
    lobby_b = endpoint_b.create_channel("/chat/:room_id", on_join)
    lobby_b.on_message("message", on_message)

    return pond_a, pond_b


async def _connect_and_join(
    pond: PondSocket, user_id: str, *, room: str = "/chat/1"
) -> InMemoryTransport:
    match = pond.match_endpoint("/api/socket")
    assert match is not None
    endpoint = match.endpoint
    transport = InMemoryTransport(user_id)
    await endpoint.register_transport(transport)
    await transport.receive_sent()
    await transport.push_inbound(
        _client_event(ClientActions.JOIN_CHANNEL.value, room, "join")
    )
    while True:
        ev = await transport.receive_sent()
        if ev.action == ServerActions.SYSTEM.value and ev.event == SystemEvents.ACKNOWLEDGE.value:
            break
    return transport


async def test_node_id_auto_generated_when_pubsub_configured() -> None:
    bus = LocalPubSub()
    pond = PondSocket(pubsub=bus)
    assert pond.options.node_id != ""
    await pond.close()
    await bus.close()


async def test_broadcast_from_node_a_reaches_user_on_node_b() -> None:
    bus = LocalPubSub()
    pond_a, pond_b = await _build_pond_pair(bus)

    alice = await _connect_and_join(pond_a, "alice")
    bob = await _connect_and_join(pond_b, "bob")

    await alice.push_inbound(
        _client_event(
            ClientActions.BROADCAST.value,
            "/chat/1",
            "message",
            {"text": "hello"},
        )
    )

    for _ in range(50):
        ev = await bob.receive_sent(wait=0.5)
        if ev.action == ServerActions.BROADCAST.value and ev.event == "message":
            assert ev.payload == {"text": "hello"}
            break
    else:
        raise AssertionError("bob did not receive cross-node broadcast")

    await pond_a.close()
    await pond_b.close()
    await bus.close()


async def test_local_users_see_their_own_broadcast() -> None:
    bus = LocalPubSub()
    pond_a, pond_b = await _build_pond_pair(bus)

    alice = await _connect_and_join(pond_a, "alice")

    await alice.push_inbound(
        _client_event(
            ClientActions.BROADCAST.value, "/chat/1", "message", {"text": "hi"}
        )
    )

    for _ in range(50):
        ev = await alice.receive_sent(wait=0.5)
        if ev.action == ServerActions.BROADCAST.value and ev.event == "message":
            assert ev.payload == {"text": "hi"}
            break
    else:
        raise AssertionError("alice did not receive own broadcast")

    await pond_a.close()
    await pond_b.close()
    await bus.close()


async def test_node_id_filter_prevents_echo() -> None:
    bus = LocalPubSub()
    pond_a, pond_b = await _build_pond_pair(bus)

    alice = await _connect_and_join(pond_a, "alice")

    await alice.push_inbound(
        _client_event(
            ClientActions.BROADCAST.value, "/chat/1", "message", {"text": "x"}
        )
    )

    received = 0
    deadline = asyncio.get_event_loop().time() + 0.5
    while asyncio.get_event_loop().time() < deadline:
        try:
            ev = await alice.receive_sent(wait=0.1)
        except TimeoutError:
            break
        if ev.action == ServerActions.BROADCAST.value and ev.event == "message":
            received += 1
    assert received == 1

    await pond_a.close()
    await pond_b.close()
    await bus.close()


async def test_presence_change_on_node_a_reaches_tracked_user_on_node_b() -> None:
    bus = LocalPubSub()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()
        await ctx.track({"id": ctx.transport.get_id()})

    pond_a = PondSocket(pubsub=bus)
    endpoint_a = pond_a.create_endpoint("/api/socket", auth)
    endpoint_a.create_channel("/chat/:room_id", on_join)

    pond_b = PondSocket(pubsub=bus)
    endpoint_b = pond_b.create_endpoint("/api/socket", auth)
    endpoint_b.create_channel("/chat/:room_id", on_join)

    bob = await _connect_and_join(pond_b, "bob")
    while True:
        ev = await bob.receive_sent(wait=0.5)
        if ev.action == ServerActions.PRESENCE.value and ev.event == PresenceEventTypes.JOIN.value:
            break

    await _connect_and_join(pond_a, "alice")

    received_remote_join = False
    for _ in range(50):
        try:
            ev = await bob.receive_sent(wait=0.2)
        except TimeoutError:
            continue
        if (
            ev.action == ServerActions.PRESENCE.value
            and ev.event == PresenceEventTypes.JOIN.value
            and isinstance(ev.payload, dict)
            and ev.payload.get("changed", {}).get("id") == "alice"
        ):
            received_remote_join = True
            break
    assert received_remote_join

    await pond_a.close()
    await pond_b.close()
    await bus.close()


async def test_disabled_pubsub_means_no_cross_node_propagation() -> None:
    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    async def on_message(ctx: EventContext) -> None:
        await ctx.broadcast(ctx.event_name, ctx.get_payload())

    pond_a = PondSocket()
    endpoint_a = pond_a.create_endpoint("/api/socket", auth)
    lobby_a = endpoint_a.create_channel("/chat/:room_id", on_join)
    lobby_a.on_message("message", on_message)

    pond_b = PondSocket()
    endpoint_b = pond_b.create_endpoint("/api/socket", auth)
    lobby_b = endpoint_b.create_channel("/chat/:room_id", on_join)
    lobby_b.on_message("message", on_message)

    alice = await _connect_and_join(pond_a, "alice")
    bob = await _connect_and_join(pond_b, "bob")

    await alice.push_inbound(
        _client_event(
            ClientActions.BROADCAST.value, "/chat/1", "message", {"text": "isolated"}
        )
    )

    received = False
    deadline = asyncio.get_event_loop().time() + 0.3
    while asyncio.get_event_loop().time() < deadline:
        try:
            ev = await bob.receive_sent(wait=0.05)
        except TimeoutError:
            continue
        if ev.action == ServerActions.BROADCAST.value and ev.event == "message":
            received = True
            break
    assert received is False

    await pond_a.close()
    await pond_b.close()
