from __future__ import annotations

import asyncio
from typing import Any

from pondsocket import ConnectionContext, InMemoryTransport, JoinContext, PondSocket
from pondsocket.types import Event
from pondsocket_common import ClientActions, IncomingConnection, ServerActions, uuid


def _join_event(channel: str) -> Event:
    return Event(
        action=ClientActions.JOIN_CHANNEL.value,
        channel_name=channel,
        request_id=uuid(),
        event="join",
        payload={},
    )


async def _connect(pond: PondSocket) -> InMemoryTransport:
    match = pond.match_endpoint("/api/socket")
    assert match is not None
    endpoint = match.endpoint
    incoming = IncomingConnection(id=uuid(), address="127.0.0.1", query={})
    ctx = await endpoint.request_connection(incoming, user_id=incoming.id)
    transport = InMemoryTransport(ctx.user_id, assigns=ctx.assigns)
    await endpoint.register_transport(transport)
    await transport.receive_sent(wait=0.5)
    return transport


async def _make_pond(handler: Any) -> PondSocket:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)
    endpoint.create_channel("/chat/:room_id", handler)
    return pond


async def _join_and_collect(pond: PondSocket) -> list[Event]:
    transport = await _connect(pond)
    await transport.push_inbound(_join_event("/chat/42"))
    await asyncio.sleep(0.05)
    return await transport.drain_outbox()


def _system_frames(frames: list[Event]) -> list[Event]:
    return [f for f in frames if f.action == ServerActions.SYSTEM.value]


async def test_accept_then_raise_sends_single_acknowledge() -> None:
    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()
        raise RuntimeError("boom after accept")

    pond = await _make_pond(on_join)
    frames = _system_frames(await _join_and_collect(pond))
    events = [f.event for f in frames]
    assert events.count("ACKNOWLEDGE") == 1
    assert "UNAUTHORIZED" not in events
    assert len(frames) == 1
    await pond.close()


async def test_decline_then_raise_sends_single_unauthorized() -> None:
    async def on_join(ctx: JoinContext) -> None:
        await ctx.decline(403, "nope")
        raise RuntimeError("boom after decline")

    pond = await _make_pond(on_join)
    frames = _system_frames(await _join_and_collect(pond))
    assert len(frames) == 1
    assert frames[0].event == "UNAUTHORIZED"
    assert frames[0].payload == {"code": 403, "message": "nope"}
    await pond.close()


async def test_raise_before_respond_sends_single_500_unauthorized() -> None:
    async def on_join(ctx: JoinContext) -> None:
        raise RuntimeError("kaboom")

    pond = await _make_pond(on_join)
    frames = _system_frames(await _join_and_collect(pond))
    assert len(frames) == 1
    assert frames[0].event == "UNAUTHORIZED"
    assert frames[0].payload["code"] == 500
    await pond.close()


async def test_decline_twice_sends_single_unauthorized() -> None:
    async def on_join(ctx: JoinContext) -> None:
        await ctx.decline(401, "first")
        await ctx.decline(403, "second")

    pond = await _make_pond(on_join)
    frames = _system_frames(await _join_and_collect(pond))
    assert len(frames) == 1
    assert frames[0].event == "UNAUTHORIZED"
    assert frames[0].payload == {"code": 401, "message": "first"}
    await pond.close()


async def test_accept_then_decline_sends_single_acknowledge() -> None:
    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()
        await ctx.decline(403, "too late")

    pond = await _make_pond(on_join)
    frames = _system_frames(await _join_and_collect(pond))
    events = [f.event for f in frames]
    assert events.count("ACKNOWLEDGE") == 1
    assert "UNAUTHORIZED" not in events
    assert len(frames) == 1
    await pond.close()


async def test_async_handler_responds_after_await_single_frame() -> None:
    async def on_join(ctx: JoinContext) -> None:
        await asyncio.sleep(0.01)
        await ctx.accept()

    pond = await _make_pond(on_join)
    frames = _system_frames(await _join_and_collect(pond))
    assert len(frames) == 1
    assert frames[0].event == "ACKNOWLEDGE"
    await pond.close()
