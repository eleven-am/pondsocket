from __future__ import annotations

import asyncio

import pytest
from pondsocket_client import Channel, ConnectionState, ResponseTimeoutError

from pondsocket_common import (
    BehaviorSubject,
    ChannelState,
    ClientMessage,
    Events,
    ServerActions,
    ServerMessage,
    uuid,
)


def _make_channel(
    *,
    initial_state: ConnectionState = ConnectionState.CONNECTED,
) -> tuple[Channel, list[ClientMessage], BehaviorSubject[ConnectionState]]:
    sent: list[ClientMessage] = []
    state: BehaviorSubject[ConnectionState] = BehaviorSubject(initial_state)

    async def publisher(msg: ClientMessage) -> None:
        sent.append(msg)

    channel = Channel(
        name="/chat/1",
        params={"user": "alice"},
        publisher=publisher,
        connection_state=state,
    )
    return channel, sent, state


def _system(event: str, channel: str = "/chat/1", payload: dict | None = None) -> ServerMessage:
    return ServerMessage(
        action=ServerActions.SYSTEM,
        channelName=channel,
        event=event,
        requestId=uuid(),
        payload=payload if payload is not None else {},
    )


async def test_late_not_found_on_joined_channel_is_ignored() -> None:
    nf, _, _ = _make_channel()
    nf.join()
    nf.handle_event(_system(Events.ACKNOWLEDGE.value))
    assert nf.channel_state == ChannelState.JOINED
    nf.handle_event(_system(Events.NOT_FOUND.value))
    assert nf.channel_state == ChannelState.JOINED

    ua, _, _ = _make_channel()
    ua.join()
    ua.handle_event(_system(Events.ACKNOWLEDGE.value))
    ua.handle_event(_system(Events.UNAUTHORIZED.value))
    assert ua.channel_state == ChannelState.JOINED


async def test_ack_then_not_found_is_ignored_and_stays_joined() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(_system(Events.ACKNOWLEDGE.value))
    channel.send_message("queued", {"n": 1})
    channel.handle_event(_system(Events.NOT_FOUND.value))
    assert channel.channel_state == ChannelState.JOINED
    assert len(channel._queue) == 0  # type: ignore[attr-defined]


async def test_duplicate_not_found_is_idempotent_no_crash() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(_system(Events.NOT_FOUND.value))
    channel.handle_event(_system(Events.NOT_FOUND.value))
    channel.handle_event(_system(Events.NOT_FOUND.value))
    assert channel.channel_state == ChannelState.DECLINED


async def test_not_found_with_empty_payload_does_not_crash() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(_system(Events.NOT_FOUND.value, payload={}))
    assert channel.channel_state == ChannelState.DECLINED


async def test_not_found_cancels_pending_response() -> None:
    channel, _, _ = _make_channel(initial_state=ConnectionState.DISCONNECTED)
    channel.join()
    task = asyncio.create_task(channel.send_for_response("ping", {}, wait=1.0))
    await asyncio.sleep(0)
    channel.handle_event(_system(Events.NOT_FOUND.value))
    with pytest.raises((asyncio.CancelledError, ResponseTimeoutError)):
        await task


async def test_declined_channel_cannot_rejoin_via_join_guard() -> None:
    channel, sent, _ = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    channel.handle_event(_system(Events.NOT_FOUND.value))
    assert channel.channel_state == ChannelState.DECLINED
    sent.clear()
    channel.join()
    await asyncio.sleep(0)
    assert channel.channel_state == ChannelState.DECLINED
    assert sent == []


async def test_declined_then_stray_acknowledge_does_not_resurrect() -> None:
    nf, _, _ = _make_channel()
    nf.join()
    nf.handle_event(_system(Events.NOT_FOUND.value))
    assert nf.channel_state == ChannelState.DECLINED
    nf.handle_event(_system(Events.ACKNOWLEDGE.value))
    assert nf.channel_state == ChannelState.DECLINED

    ua, _, _ = _make_channel()
    ua.join()
    ua.handle_event(_system(Events.UNAUTHORIZED.value))
    ua.handle_event(_system(Events.ACKNOWLEDGE.value))
    assert ua.channel_state == ChannelState.DECLINED
