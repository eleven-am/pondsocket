from __future__ import annotations

import asyncio

import pytest
from pondsocket_client import Channel, ConnectionState, ResponseTimeoutError

from pondsocket_common import (
    BehaviorSubject,
    ChannelState,
    ClientMessage,
    Events,
    PresenceEventTypes,
    PresenceMessage,
    ServerActions,
    ServerMessage,
    uuid,
)


def _make_channel() -> tuple[Channel, list[ClientMessage], BehaviorSubject[ConnectionState]]:
    sent: list[ClientMessage] = []
    state: BehaviorSubject[ConnectionState] = BehaviorSubject(ConnectionState.CONNECTED)

    async def publisher(msg: ClientMessage) -> None:
        sent.append(msg)

    channel = Channel(name="/chat/1", params={}, publisher=publisher, connection_state=state)
    return channel, sent, state


def _system(event: str, request_id: str, payload: dict | None = None) -> ServerMessage:
    return ServerMessage(
        action=ServerActions.SYSTEM,
        channelName="/chat/1",
        event=event,
        requestId=request_id,
        payload=payload if payload is not None else {},
    )


def _broadcast(event: str, request_id: str, payload: dict | None = None) -> ServerMessage:
    return ServerMessage(
        action=ServerActions.BROADCAST,
        channelName="/chat/1",
        event=event,
        requestId=request_id,
        payload=payload if payload is not None else {},
    )


async def _join(channel: Channel) -> None:
    channel.join()
    await asyncio.sleep(0)
    channel.handle_event(_system(Events.ACKNOWLEDGE.value, uuid()))
    assert channel.channel_state == ChannelState.JOINED


async def _wait_request_id(sent: list[ClientMessage], event: str) -> str:
    for _ in range(100):
        for msg in reversed(sent):
            if msg.event == event:
                return msg.request_id
        await asyncio.sleep(0.001)
    raise AssertionError(f"no sent message for event {event!r}")


@pytest.mark.parametrize("reserved", ["ACKNOWLEDGE", "UNAUTHORIZED", "NOT_FOUND"])
async def test_reply_with_reserved_event_name_resolves_request(reserved: str) -> None:
    channel, sent, _ = _make_channel()
    await _join(channel)
    task = asyncio.create_task(channel.send_for_response("lookup", {}, wait=0.2))
    await asyncio.sleep(0)
    rid = await _wait_request_id(sent, "lookup")
    channel.handle_event(_system(reserved, rid, payload={"result": "here"}))
    result = await task
    assert result == {"result": "here"}


async def test_reply_with_reserved_name_resolves_and_keeps_channel_joined() -> None:
    channel, sent, _ = _make_channel()
    await _join(channel)
    task = asyncio.create_task(channel.send_for_response("lookup", {}, wait=0.2))
    await asyncio.sleep(0)
    rid = await _wait_request_id(sent, "lookup")
    channel.handle_event(_system("NOT_FOUND", rid, payload={"result": "here"}))
    result = await task
    assert result == {"result": "here"}
    assert channel.channel_state == ChannelState.JOINED


async def test_late_response_after_timeout_leaks_into_on_message() -> None:
    channel, sent, _ = _make_channel()
    await _join(channel)
    received: list[tuple[str, dict]] = []
    channel.on_message(lambda m: received.append((m.event, dict(m.payload))))
    with pytest.raises(ResponseTimeoutError):
        await channel.send_for_response("ping", {}, wait=0.05)
    rid = await _wait_request_id(sent, "ping")
    channel.handle_event(_broadcast("ping", rid, payload={"late": True}))
    await asyncio.sleep(0)
    assert received == [("ping", {"late": True})]


async def test_presence_before_acknowledge_updates_snapshot_but_drops_callbacks() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    joins: list[dict] = []
    users: list[list[dict]] = []
    channel.on_join(joins.append)
    channel.on_users_change(users.append)
    channel.handle_event(
        PresenceMessage(
            action=ServerActions.PRESENCE,
            channelName="/chat/1",
            event=PresenceEventTypes.JOIN,
            requestId=uuid(),
            payload={"presence": [{"id": "x"}], "changed": {"id": "x"}},
        )
    )
    assert channel.presence == [{"id": "x"}]
    assert joins == []
    assert users == []
    channel.handle_event(_system(Events.ACKNOWLEDGE.value, uuid()))
    assert joins == []
    assert users == []
