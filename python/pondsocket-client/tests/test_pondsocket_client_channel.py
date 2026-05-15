from __future__ import annotations

import asyncio

import pytest
from pondsocket_client import Channel, ConnectionState, ResponseTimeoutError

from pondsocket_common import (
    BehaviorSubject,
    ChannelState,
    ClientActions,
    ClientMessage,
    Events,
    PresenceEventTypes,
    PresenceMessage,
    ServerActions,
    ServerMessage,
    uuid,
)


def _server_msg(
    channel: str,
    event: str,
    *,
    action: ServerActions = ServerActions.BROADCAST,
    request_id: str | None = None,
    payload: dict | None = None,
) -> ServerMessage:
    return ServerMessage(
        action=action,
        channelName=channel,
        event=event,
        requestId=request_id or uuid(),
        payload=payload or {},
    )


def _presence_msg(
    channel: str, event: PresenceEventTypes, presence: list[dict], changed: dict
) -> PresenceMessage:
    return PresenceMessage(
        action=ServerActions.PRESENCE,
        channelName=channel,
        event=event,
        requestId=uuid(),
        payload={"presence": presence, "changed": changed},
    )


def _make_channel(
    *,
    initial_state: ConnectionState = ConnectionState.CONNECTED,
    queue_size: int = 100,
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
        max_queue_size=queue_size,
    )
    return channel, sent, state


async def test_initial_state_is_idle() -> None:
    channel, _, _ = _make_channel()
    assert channel.channel_state == ChannelState.IDLE
    assert channel.presence == []


async def test_join_when_connected_transitions_to_joining_and_sends() -> None:
    channel, sent, _ = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    assert channel.channel_state == ChannelState.JOINING
    assert len(sent) == 1
    assert sent[0].action == ClientActions.JOIN_CHANNEL
    assert sent[0].channel_name == "/chat/1"
    assert sent[0].payload == {"user": "alice"}


async def test_join_when_disconnected_queues_until_connected() -> None:
    channel, sent, state = _make_channel(initial_state=ConnectionState.DISCONNECTED)
    channel.join()
    await asyncio.sleep(0)
    assert sent == []
    state.publish(ConnectionState.CONNECTED)
    await asyncio.sleep(0)
    assert len(sent) == 1
    assert sent[0].action == ClientActions.JOIN_CHANNEL


async def test_acknowledge_transitions_to_joined_and_flushes_queue() -> None:
    channel, sent, state = _make_channel(initial_state=ConnectionState.DISCONNECTED)
    channel.join()
    channel.send_message("hello", {"text": "hi"})
    state.publish(ConnectionState.CONNECTED)
    await asyncio.sleep(0)
    sent.clear()
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
        )
    )
    assert channel.channel_state == ChannelState.JOINED


async def test_unauthorized_transitions_to_declined() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.UNAUTHORIZED.value,
            action=ServerActions.SYSTEM,
        )
    )
    assert channel.channel_state == ChannelState.DECLINED


async def test_send_message_before_joined_is_queued() -> None:
    channel, sent, _ = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    sent.clear()
    channel.send_message("ping", {})
    await asyncio.sleep(0)
    assert sent == []
    channel.handle_event(
        _server_msg(
            "/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM
        )
    )
    await asyncio.sleep(0)
    assert len(sent) == 1
    assert sent[0].event == "ping"


async def test_on_message_fires_for_broadcasts() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg(
            "/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM
        )
    )
    received: list[str] = []

    def cb(msg: ServerMessage) -> None:
        received.append(msg.event)

    channel.on_message(cb)
    channel.handle_event(_server_msg("/chat/1", "chat"))
    channel.handle_event(_server_msg("/chat/1", "ping"))
    assert received == ["chat", "ping"]


async def test_on_message_event_filters_by_event_name() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg(
            "/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM
        )
    )
    received: list[str] = []
    channel.on_message_event("chat", lambda m: received.append(m.event))
    channel.handle_event(_server_msg("/chat/1", "chat"))
    channel.handle_event(_server_msg("/chat/1", "ping"))
    assert received == ["chat"]


async def test_presence_join_updates_local_snapshot() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    channel.handle_event(
        _presence_msg(
            "/chat/1",
            PresenceEventTypes.JOIN,
            presence=[{"id": "alice"}, {"id": "bob"}],
            changed={"id": "alice"},
        )
    )
    assert channel.presence == [{"id": "alice"}, {"id": "bob"}]


async def test_on_join_fires_with_changed_user() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    joined: list[dict] = []
    channel.on_join(joined.append)
    channel.handle_event(
        _presence_msg(
            "/chat/1",
            PresenceEventTypes.JOIN,
            presence=[{"id": "alice"}],
            changed={"id": "alice"},
        )
    )
    assert joined == [{"id": "alice"}]


async def test_on_users_change_fires_for_every_presence_kind() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    snapshots: list[list[dict]] = []
    channel.on_users_change(snapshots.append)
    for kind in (
        PresenceEventTypes.JOIN,
        PresenceEventTypes.UPDATE,
        PresenceEventTypes.LEAVE,
    ):
        channel.handle_event(
            _presence_msg(
                "/chat/1", kind, presence=[{"id": "alice"}], changed={"id": "alice"}
            )
        )
    assert len(snapshots) == 3


async def test_send_for_response_correlates_by_request_id() -> None:
    channel, sent, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )

    async def respond() -> None:
        await asyncio.sleep(0.01)
        rid = sent[-1].request_id
        channel.handle_event(
            _server_msg("/chat/1", "pong", request_id=rid, payload={"echo": "hi"})
        )

    task = asyncio.create_task(respond())
    result = await channel.send_for_response("ping", {"text": "hi"}, wait=1.0)
    await task
    assert result == {"echo": "hi"}


async def test_send_for_response_times_out() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    with pytest.raises(ResponseTimeoutError):
        await channel.send_for_response("ping", {}, wait=0.05)


async def test_send_for_response_does_not_match_unrelated_event_with_same_name() -> None:
    channel, _sent, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )

    async def attempt() -> dict:
        return await channel.send_for_response("ping", {}, wait=0.2)

    task = asyncio.create_task(attempt())
    await asyncio.sleep(0.02)
    other_rid = uuid()
    channel.handle_event(
        _server_msg("/chat/1", "ping", request_id=other_rid, payload={"wrong": True})
    )
    with pytest.raises(ResponseTimeoutError):
        await task


async def test_connection_lost_transitions_joined_to_stalled() -> None:
    channel, _, state = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    assert channel.channel_state == ChannelState.JOINED
    state.publish(ConnectionState.DISCONNECTED)
    await asyncio.sleep(0)
    assert channel.channel_state == ChannelState.STALLED


async def test_reconnect_after_stalled_resends_join() -> None:
    channel, sent, state = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    state.publish(ConnectionState.DISCONNECTED)
    await asyncio.sleep(0)
    sent.clear()
    state.publish(ConnectionState.CONNECTED)
    await asyncio.sleep(0)
    assert any(m.action == ClientActions.JOIN_CHANNEL for m in sent)


async def test_outbound_queue_is_bounded() -> None:
    channel, _sent, _ = _make_channel(
        initial_state=ConnectionState.DISCONNECTED, queue_size=3
    )
    channel.join()
    for i in range(10):
        channel.send_message(f"e{i}", {"i": i})
    assert len(channel._queue) == 3  # type: ignore[attr-defined]


async def test_leave_transitions_to_closed_and_sends() -> None:
    channel, sent, _ = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    await asyncio.sleep(0)
    sent.clear()
    channel.leave()
    await asyncio.sleep(0)
    assert channel.channel_state == ChannelState.CLOSED
    assert len(sent) == 1
    assert sent[0].action == ClientActions.LEAVE_CHANNEL


async def test_messages_after_leave_are_ignored() -> None:
    channel, sent, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    channel.leave()
    await asyncio.sleep(0)
    sent.clear()
    channel.send_message("noop", {})
    await asyncio.sleep(0)
    assert sent == []


async def test_state_change_callback_fires_immediately_with_current_state() -> None:
    channel, _, _ = _make_channel()
    seen: list[ChannelState] = []
    channel.on_channel_state_change(seen.append)
    assert seen == [ChannelState.IDLE]
    channel.join()
    assert seen[-1] == ChannelState.JOINING
