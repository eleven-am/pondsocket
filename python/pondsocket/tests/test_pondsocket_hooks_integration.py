from __future__ import annotations

import asyncio
from typing import Any

from pondsocket import (
    ConnectionContext,
    EventContext,
    Hooks,
    InMemoryTransport,
    JoinContext,
    NoopMetrics,
    Options,
    PondSocket,
    SystemEvents,
    Transport,
    User,
)
from pondsocket.types import Event
from pondsocket_common import ClientActions, ServerActions, uuid


class RecordingMetrics(NoopMetrics):
    __slots__ = ("calls",)

    def __init__(self) -> None:
        self.calls: list[tuple[str, tuple[Any, ...]]] = []

    def connection_opened(self, conn_id: str, endpoint: str) -> None:
        self.calls.append(("connection_opened", (conn_id, endpoint)))

    def connection_closed(self, conn_id: str, duration_seconds: float) -> None:
        self.calls.append(("connection_closed", (conn_id, duration_seconds)))

    def channel_created(self, channel: str) -> None:
        self.calls.append(("channel_created", (channel,)))

    def channel_destroyed(self, channel: str) -> None:
        self.calls.append(("channel_destroyed", (channel,)))

    def channel_joined(self, user_id: str, channel: str) -> None:
        self.calls.append(("channel_joined", (user_id, channel)))

    def channel_left(self, user_id: str, channel: str) -> None:
        self.calls.append(("channel_left", (user_id, channel)))


def _client_event(action: str, channel: str, event: str) -> Event:
    return Event(
        action=action,
        channel_name=channel,
        request_id=uuid(),
        event=event,
        payload={},
    )


async def _pond_with_hooks(hooks: Hooks) -> tuple[PondSocket, list[str]]:
    captured: list[str] = []

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    async def on_message(ctx: EventContext) -> None:
        captured.append(ctx.event_name)

    pond = PondSocket(options=Options(hooks=hooks))
    endpoint = pond.create_endpoint("/api", auth)
    lobby = endpoint.create_channel("/chat/:room_id", on_join)
    lobby.on_message("message", on_message)
    return pond, captured


async def _connect(pond: PondSocket, user_id: str = "u1") -> InMemoryTransport:
    match = pond.match_endpoint("/api")
    assert match is not None
    transport = InMemoryTransport(user_id)
    await match.endpoint.register_transport(transport)
    while True:
        ev = await transport.receive_sent()
        if ev.action == ServerActions.CONNECT.value:
            break
    return transport


async def test_metrics_connection_opened_fires_on_register() -> None:
    metrics = RecordingMetrics()
    pond, _ = await _pond_with_hooks(Hooks(metrics=metrics))
    transport = await _connect(pond, "alice")
    assert ("connection_opened", ("alice", "/api")) in metrics.calls
    await transport.close()


async def test_metrics_connection_closed_fires_on_transport_close() -> None:
    metrics = RecordingMetrics()
    pond, _ = await _pond_with_hooks(Hooks(metrics=metrics))
    transport = await _connect(pond, "alice")
    await transport.close()

    for _ in range(50):
        if any(c[0] == "connection_closed" for c in metrics.calls):
            break
        await asyncio.sleep(0.01)

    closed_calls = [c for c in metrics.calls if c[0] == "connection_closed"]
    assert len(closed_calls) == 1
    user_id, duration = closed_calls[0][1]
    assert user_id == "alice"
    assert duration >= 0.0


async def test_on_connect_hook_receives_transport() -> None:
    seen: list[str] = []

    async def on_connect(t: Transport) -> None:
        seen.append(t.get_id())

    pond, _ = await _pond_with_hooks(Hooks(on_connect=on_connect))
    transport = await _connect(pond, "alice")
    await asyncio.sleep(0.01)
    assert seen == ["alice"]
    await transport.close()


async def test_on_disconnect_hook_fires_on_close() -> None:
    seen: list[str] = []

    async def on_disconnect(t: Transport) -> None:
        seen.append(t.get_id())

    pond, _ = await _pond_with_hooks(Hooks(on_disconnect=on_disconnect))
    transport = await _connect(pond, "alice")
    await transport.close()

    for _ in range(50):
        if seen:
            break
        await asyncio.sleep(0.01)
    assert seen == ["alice"]


async def test_metrics_channel_created_and_joined_fire_on_join() -> None:
    metrics = RecordingMetrics()
    pond, _ = await _pond_with_hooks(Hooks(metrics=metrics))
    transport = await _connect(pond, "alice")
    await transport.push_inbound(
        _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    )

    for _ in range(50):
        ev = await transport.receive_sent(wait=0.5)
        if ev.action == ServerActions.SYSTEM.value and ev.event == SystemEvents.ACKNOWLEDGE.value:
            break

    created = [c for c in metrics.calls if c[0] == "channel_created"]
    joined = [c for c in metrics.calls if c[0] == "channel_joined"]
    assert created == [("channel_created", ("/chat/42",))]
    assert joined == [("channel_joined", ("alice", "/chat/42"))]
    await transport.close()


async def test_metrics_channel_left_fires_on_explicit_leave() -> None:
    metrics = RecordingMetrics()
    pond, _ = await _pond_with_hooks(Hooks(metrics=metrics))
    transport = await _connect(pond, "alice")
    await transport.push_inbound(
        _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    )
    for _ in range(50):
        ev = await transport.receive_sent(wait=0.5)
        if ev.event == SystemEvents.ACKNOWLEDGE.value:
            break

    await transport.push_inbound(
        _client_event(ClientActions.LEAVE_CHANNEL.value, "/chat/42", "leave")
    )

    for _ in range(50):
        if any(c[0] == "channel_left" for c in metrics.calls):
            break
        await asyncio.sleep(0.01)

    left = [c for c in metrics.calls if c[0] == "channel_left"]
    assert left == [("channel_left", ("alice", "/chat/42"))]
    await transport.close()


async def test_before_join_and_after_join_hooks_fire_on_acceptance() -> None:
    seen_before: list[tuple[str, str]] = []
    seen_after: list[tuple[str, str]] = []

    async def before_join(user: User, channel: str) -> None:
        seen_before.append((user.id, channel))

    async def after_join(user: User, channel: str) -> None:
        seen_after.append((user.id, channel))

    pond, _ = await _pond_with_hooks(
        Hooks(before_join=before_join, after_join=after_join)
    )
    transport = await _connect(pond, "alice")
    await transport.push_inbound(
        _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    )
    for _ in range(50):
        ev = await transport.receive_sent(wait=0.5)
        if ev.event == SystemEvents.ACKNOWLEDGE.value:
            break

    assert seen_before == [("alice", "/chat/42")]
    assert seen_after == [("alice", "/chat/42")]
    await transport.close()


async def test_after_join_not_fired_when_declined() -> None:
    seen_before: list[tuple[str, str]] = []
    seen_after: list[tuple[str, str]] = []

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    async def on_join(ctx: JoinContext) -> None:
        await ctx.decline(403, "no")

    async def before_join(user: User, channel: str) -> None:
        seen_before.append((user.id, channel))

    async def after_join(user: User, channel: str) -> None:
        seen_after.append((user.id, channel))

    pond = PondSocket(
        options=Options(
            hooks=Hooks(before_join=before_join, after_join=after_join)
        )
    )
    endpoint = pond.create_endpoint("/api", auth)
    endpoint.create_channel("/chat/:room_id", on_join)

    transport = await _connect(pond, "alice")
    await transport.push_inbound(
        _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    )
    for _ in range(50):
        ev = await transport.receive_sent(wait=0.5)
        if ev.event == SystemEvents.UNAUTHORIZED.value:
            break

    assert seen_before == [("alice", "/chat/42")]
    assert seen_after == []
    await transport.close()


async def test_before_message_and_after_message_hooks_fire_around_handler() -> None:
    seen_before: list[str] = []
    seen_after: list[tuple[str, bool]] = []

    async def before_message(event: Event) -> None:
        seen_before.append(event.event)

    async def after_message(event: Event, err: BaseException | None) -> None:
        seen_after.append((event.event, err is not None))

    pond, _ = await _pond_with_hooks(
        Hooks(before_message=before_message, after_message=after_message)
    )
    transport = await _connect(pond, "alice")
    await transport.push_inbound(
        _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    )
    for _ in range(50):
        ev = await transport.receive_sent(wait=0.5)
        if ev.event == SystemEvents.ACKNOWLEDGE.value:
            break

    await transport.push_inbound(
        _client_event(ClientActions.BROADCAST.value, "/chat/42", "message")
    )

    for _ in range(50):
        if seen_after:
            break
        await asyncio.sleep(0.01)

    assert "message" in seen_before
    assert seen_after == [("message", False)]
    await transport.close()


async def test_before_message_raising_drops_handler_and_after_sees_error() -> None:
    seen_after: list[tuple[str, type | None]] = []

    async def before_message(event: Event) -> None:
        if event.event == "block_me":
            raise RuntimeError("nope")

    async def after_message(event: Event, err: BaseException | None) -> None:
        seen_after.append((event.event, type(err) if err else None))

    pond, captured = await _pond_with_hooks(
        Hooks(before_message=before_message, after_message=after_message)
    )
    transport = await _connect(pond, "alice")
    await transport.push_inbound(
        _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/42", "join")
    )
    for _ in range(50):
        ev = await transport.receive_sent(wait=0.5)
        if ev.event == SystemEvents.ACKNOWLEDGE.value:
            break

    await transport.push_inbound(
        _client_event(ClientActions.BROADCAST.value, "/chat/42", "block_me")
    )

    for _ in range(50):
        if seen_after:
            break
        await asyncio.sleep(0.01)

    assert seen_after == [("block_me", RuntimeError)]
    assert "block_me" not in captured
    await transport.close()


async def test_hooks_dont_break_flow_when_they_raise() -> None:
    async def flaky_on_connect(_t: Transport) -> None:
        raise RuntimeError("boom")

    pond, _ = await _pond_with_hooks(Hooks(on_connect=flaky_on_connect))
    transport = await _connect(pond, "alice")
    assert transport.is_active()
    await transport.close()
