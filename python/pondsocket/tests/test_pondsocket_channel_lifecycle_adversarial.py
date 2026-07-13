from __future__ import annotations

import asyncio
from collections.abc import Awaitable, Callable
from typing import Any

import pytest

from pondsocket import (
    ConnectionContext,
    InMemoryTransport,
    JoinContext,
    OutgoingContext,
    PondSocket,
)
from pondsocket.channel import Channel, ChannelOptions
from pondsocket.types import Event, Options, SystemEvents
from pondsocket_common import ClientActions, IncomingConnection, ServerActions, uuid


def _client_event(action: str, channel: str, event: str) -> Event:
    return Event(
        action=action,
        channel_name=channel,
        request_id=uuid(),
        event=event,
        payload={},
    )


async def _connect(pond: PondSocket, user_id: str) -> InMemoryTransport:
    match = pond.match_endpoint("/api/socket")
    assert match is not None
    incoming = IncomingConnection(id=user_id, address="127.0.0.1", query={})
    ctx = await match.endpoint.request_connection(incoming, user_id=user_id)
    assert ctx.is_accepted
    transport = InMemoryTransport(ctx.user_id, assigns=ctx.assigns)
    await match.endpoint.register_transport(transport)
    await transport.receive_sent()
    return transport


async def _expect_event(transport: InMemoryTransport, event_name: str) -> Event:
    for _ in range(20):
        event = await transport.receive_sent()
        if event.action == ServerActions.SYSTEM.value and event.event == event_name:
            return event
    raise AssertionError(f"did not receive system event {event_name}")


def _dispatcher(channel: Channel) -> asyncio.Task[None]:
    task = channel._dispatch_task  # type: ignore[attr-defined]
    assert task is not None
    return task


async def _pond_with_channel(
    on_join: Callable[[JoinContext], Awaitable[None]],
) -> tuple[PondSocket, Any]:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/api/socket", auth)
    lobby = endpoint.create_channel("/chat/:room", on_join)
    return pond, lobby


@pytest.mark.asyncio
async def test_last_user_removal_completes_dispatcher_and_destroys_once() -> None:
    destroyed = 0

    async def on_destroy() -> None:
        nonlocal destroyed
        destroyed += 1

    channel = Channel(
        ChannelOptions(
            name="/chat/1",
            options=Options(),
            on_destroy=on_destroy,
        )
    )
    await channel.start()
    transport = InMemoryTransport("alice")
    await channel.add_user(transport)
    dispatcher = _dispatcher(channel)

    assert await channel.remove_user("alice")

    assert not channel.is_active
    assert dispatcher.done()
    assert channel._dispatch_task is None  # type: ignore[attr-defined]
    assert destroyed == 1

    await asyncio.gather(channel.close(), channel.close())
    assert destroyed == 1


@pytest.mark.asyncio
async def test_concurrent_close_and_recursive_destroy_complete_once() -> None:
    destroyed = 0
    channel: Channel

    async def on_destroy() -> None:
        nonlocal destroyed
        destroyed += 1
        await channel.close()

    channel = Channel(
        ChannelOptions(
            name="/chat/1",
            options=Options(),
            on_destroy=on_destroy,
        )
    )
    await channel.start()
    dispatcher = _dispatcher(channel)

    async with asyncio.timeout(1.0):
        await asyncio.gather(channel.close(), channel.close(), channel.close())

    assert dispatcher.done()
    assert destroyed == 1


@pytest.mark.asyncio
async def test_explicit_leave_completes_channel_dispatcher() -> None:
    channels: list[Channel] = []

    async def on_join(ctx: JoinContext) -> None:
        channels.append(ctx.channel)
        await ctx.accept()

    pond, lobby = await _pond_with_channel(on_join)
    transport = await _connect(pond, "alice")
    await transport.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(transport, SystemEvents.ACKNOWLEDGE.value)
    dispatcher = _dispatcher(channels[0])

    await transport.push_inbound(
        _client_event(ClientActions.LEAVE_CHANNEL.value, "/chat/1", "leave")
    )
    await _expect_event(transport, SystemEvents.EXIT_ACKNOWLEDGE.value)
    await asyncio.wait_for(asyncio.shield(dispatcher), timeout=1.0)

    assert not channels[0].is_active
    assert await lobby.get_channel("/chat/1") is None
    await pond.close()


@pytest.mark.asyncio
async def test_abrupt_disconnect_completes_channel_dispatcher() -> None:
    channels: list[Channel] = []

    async def on_join(ctx: JoinContext) -> None:
        channels.append(ctx.channel)
        await ctx.accept()

    pond, lobby = await _pond_with_channel(on_join)
    transport = await _connect(pond, "alice")
    await transport.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(transport, SystemEvents.ACKNOWLEDGE.value)
    dispatcher = _dispatcher(channels[0])

    await transport.close()

    assert dispatcher.done()
    assert not channels[0].is_active
    assert await lobby.get_channel("/chat/1") is None
    await pond.close()


@pytest.mark.asyncio
async def test_dispatcher_can_trigger_last_user_close_without_self_await() -> None:
    channels: list[Channel] = []

    async def on_join(ctx: JoinContext) -> None:
        channels.append(ctx.channel)
        await ctx.accept()

    pond, lobby = await _pond_with_channel(on_join)

    async def remove_recipient(ctx: OutgoingContext) -> None:
        await ctx.channel.remove_user(ctx.user.id)

    lobby.on_outgoing("message", remove_recipient)
    transport = await _connect(pond, "alice")
    await transport.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(transport, SystemEvents.ACKNOWLEDGE.value)
    channel = channels[0]
    dispatcher = _dispatcher(channel)

    await channel.broadcast("message", {"text": "close"})
    done, pending = await asyncio.wait({dispatcher}, timeout=1.0)
    assert done == {dispatcher}
    assert not pending
    close_task = channel._close_task  # type: ignore[attr-defined]
    assert close_task is not None
    await asyncio.wait_for(asyncio.shield(close_task), timeout=1.0)

    assert not channel.is_active
    assert await lobby.get_channel("/chat/1") is None
    await pond.close()


@pytest.mark.asyncio
@pytest.mark.parametrize("failure", ["decline", "raise", "no_response"])
async def test_failed_join_completes_unused_channel_dispatcher(failure: str) -> None:
    channels: list[Channel] = []
    dispatchers: list[asyncio.Task[None]] = []

    async def on_join(ctx: JoinContext) -> None:
        channels.append(ctx.channel)
        dispatchers.append(_dispatcher(ctx.channel))
        if failure == "decline":
            await ctx.decline(403, "declined")
        elif failure == "raise":
            raise RuntimeError("join failed")

    pond, lobby = await _pond_with_channel(on_join)
    transport = await _connect(pond, "alice")
    await transport.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(transport, SystemEvents.UNAUTHORIZED.value)
    await asyncio.wait_for(asyncio.shield(dispatchers[0]), timeout=1.0)

    assert not channels[0].is_active
    assert await lobby.get_channel("/chat/1") is None
    await pond.close()


@pytest.mark.asyncio
async def test_repeated_connect_disconnect_reaps_every_dispatcher() -> None:
    channels: list[Channel] = []

    async def on_join(ctx: JoinContext) -> None:
        channels.append(ctx.channel)
        await ctx.accept()

    pond, lobby = await _pond_with_channel(on_join)
    dispatchers: list[asyncio.Task[None]] = []

    for index in range(10):
        transport = await _connect(pond, f"user-{index}")
        await transport.push_inbound(
            _client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join")
        )
        await _expect_event(transport, SystemEvents.ACKNOWLEDGE.value)
        dispatchers.append(_dispatcher(channels[-1]))
        await transport.close()
        assert dispatchers[-1].done()
        assert await lobby.get_channel("/chat/1") is None

    assert all(task.done() for task in dispatchers)
    await pond.close()


@pytest.mark.asyncio
async def test_pondsocket_close_completes_active_channel_dispatcher() -> None:
    channels: list[Channel] = []

    async def on_join(ctx: JoinContext) -> None:
        channels.append(ctx.channel)
        await ctx.accept()

    pond, _ = await _pond_with_channel(on_join)
    transport = await _connect(pond, "alice")
    await transport.push_inbound(_client_event(ClientActions.JOIN_CHANNEL.value, "/chat/1", "join"))
    await _expect_event(transport, SystemEvents.ACKNOWLEDGE.value)
    dispatcher = _dispatcher(channels[0])

    await pond.close()

    assert dispatcher.done()
    assert not channels[0].is_active
