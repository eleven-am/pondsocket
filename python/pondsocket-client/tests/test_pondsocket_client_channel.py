from __future__ import annotations

import asyncio
from dataclasses import dataclass
from typing import TypedDict

import pytest
from pondsocket_client import (
    Channel,
    ChannelQueueFullError,
    ConnectionState,
    ResponseTimeoutError,
    typed_channel,
)

from pondsocket_common import (
    BehaviorSubject,
    ChannelState,
    ClientActions,
    ClientMessage,
    Events,
    PondEvent,
    PondSchema,
    PresenceEventTypes,
    PresenceMessage,
    ServerActions,
    ServerMessage,
    pond_event,
    pond_schema,
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


class TypedChatPayload(TypedDict):
    text: str


class TypedPresence(TypedDict):
    user: str


class TypedAssigns(TypedDict):
    role: str


class TypedJoinParams(TypedDict):
    token: str


async def test_typed_channel_wraps_existing_channel_api() -> None:
    channel, sent, _ = _make_channel()
    schema: PondSchema[TypedPresence, TypedAssigns, TypedJoinParams] = pond_schema("chat")
    chat: PondEvent[TypedChatPayload, TypedChatPayload] = pond_event("chat")
    typed = typed_channel(channel, schema)
    received: list[TypedChatPayload] = []

    channel.join()
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
        )
    )
    typed.on(chat, received.append)
    typed.send(chat, {"text": "hello"})
    await asyncio.sleep(0)
    assert sent[-1].event == "chat"
    assert sent[-1].payload == {"text": "hello"}

    channel.handle_event(_server_msg("/chat/1", "chat", payload={"text": "from server"}))
    assert received == [{"text": "from server"}]

    channel.handle_event(
        _presence_msg(
            "/chat/1",
            PresenceEventTypes.JOIN,
            presence=[{"user": "alice"}],
            changed={"user": "alice"},
        )
    )
    assert typed.presence == [{"user": "alice"}]


@dataclass(frozen=True)
class ChatDataclass:
    text: str


@dataclass(frozen=True)
class PresenceDataclass:
    user: str


async def test_typed_channel_encodes_and_decodes_dataclass_payloads() -> None:
    channel, sent, _ = _make_channel()
    schema: PondSchema[PresenceDataclass, TypedAssigns, TypedJoinParams] = pond_schema(
        "chat", PresenceDataclass
    )
    chat: PondEvent[ChatDataclass, ChatDataclass] = pond_event("chat", ChatDataclass, ChatDataclass)
    typed = typed_channel(channel, schema)
    received: list[ChatDataclass] = []

    channel.join()
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
        )
    )
    typed.on(chat, received.append)
    typed.send(chat, ChatDataclass(text="hello"))
    await asyncio.sleep(0)
    assert sent[-1].event == "chat"
    assert sent[-1].payload == {"text": "hello"}

    channel.handle_event(_server_msg("/chat/1", "chat", payload={"text": "from server"}))
    assert received == [ChatDataclass(text="from server")]
    assert isinstance(received[0], ChatDataclass)

    channel.handle_event(
        _presence_msg(
            "/chat/1",
            PresenceEventTypes.JOIN,
            presence=[{"user": "alice"}],
            changed={"user": "alice"},
        )
    )
    assert typed.presence == [PresenceDataclass(user="alice")]
    assert isinstance(typed.presence[0], PresenceDataclass)


async def test_typed_channel_exposes_state_and_all_message_surface() -> None:
    channel, _, _ = _make_channel()
    schema: PondSchema[TypedPresence, TypedAssigns, TypedJoinParams] = pond_schema("chat")
    typed = typed_channel(channel, schema)

    states: list[ChannelState] = []
    typed.on_channel_state_change(states.append)
    assert states == [ChannelState.IDLE]
    assert typed.channel_state == ChannelState.IDLE

    typed.join()
    assert typed.channel_state == ChannelState.JOINING
    assert states[-1] == ChannelState.JOINING

    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    events: list[str] = []
    typed.on_message(lambda m: events.append(m.event))
    channel.handle_event(_server_msg("/chat/1", "chat"))
    channel.handle_event(_server_msg("/chat/1", "ping"))
    assert events == ["chat", "ping"]


async def test_typed_channel_request_decodes_dataclass_response() -> None:
    channel, sent, _ = _make_channel()
    schema: PondSchema[PresenceDataclass, TypedAssigns, TypedJoinParams] = pond_schema(
        "chat", PresenceDataclass
    )
    ping: PondEvent[ChatDataclass, ChatDataclass] = pond_event("ping", ChatDataclass, ChatDataclass)
    typed = typed_channel(channel, schema)
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )

    async def respond() -> None:
        await asyncio.sleep(0.01)
        rid = sent[-1].request_id
        channel.handle_event(
            _server_msg("/chat/1", "pong", request_id=rid, payload={"text": "pong"})
        )

    task = asyncio.create_task(respond())
    response = await typed.request(ping, ChatDataclass(text="ping"), wait=1.0)
    await task
    assert response == ChatDataclass(text="pong")
    assert isinstance(response, ChatDataclass)


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


async def test_not_found_transitions_to_declined() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.NOT_FOUND.value,
            action=ServerActions.SYSTEM,
        )
    )
    assert channel.channel_state == ChannelState.DECLINED


async def test_not_found_clears_queue_and_cancels_pending() -> None:
    channel, _, _ = _make_channel(initial_state=ConnectionState.DISCONNECTED)
    channel.join()
    channel.send_message("queued", {"n": 1})
    task = asyncio.create_task(channel.send_for_response("ping", {}, wait=1.0))
    await asyncio.sleep(0)
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.NOT_FOUND.value,
            action=ServerActions.SYSTEM,
        )
    )
    assert channel.channel_state == ChannelState.DECLINED
    assert len(channel._queue) == 0  # type: ignore[attr-defined]
    with pytest.raises((asyncio.CancelledError, ResponseTimeoutError)):
        await task


async def test_send_message_before_joined_is_queued() -> None:
    channel, sent, _ = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    sent.clear()
    channel.send_message("ping", {})
    await asyncio.sleep(0)
    assert sent == []
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    await asyncio.sleep(0)
    assert len(sent) == 1
    assert sent[0].event == "ping"


async def test_on_message_fires_for_broadcasts() -> None:
    channel, _, _ = _make_channel()
    channel.join()
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
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
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
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
            _presence_msg("/chat/1", kind, presence=[{"id": "alice"}], changed={"id": "alice"})
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
        channel.handle_event(_server_msg("/chat/1", "pong", request_id=rid, payload={"echo": "hi"}))

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


async def test_disconnect_while_joining_retries_join_before_messages() -> None:
    channel, sent, state = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    assert [msg.action for msg in sent] == [ClientActions.JOIN_CHANNEL]

    state.publish(ConnectionState.DISCONNECTED)
    channel.send_message("queued", {"i": 1})
    sent.clear()
    state.publish(ConnectionState.CONNECTED)
    await asyncio.sleep(0)

    assert channel.channel_state == ChannelState.STALLED
    assert [msg.action for msg in sent] == [ClientActions.JOIN_CHANNEL]
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
            request_id=sent[0].request_id,
        )
    )
    await asyncio.sleep(0)
    assert [msg.event for msg in sent] == [
        ClientActions.JOIN_CHANNEL.value,
        "queued",
    ]


async def test_outbound_queue_is_bounded() -> None:
    channel, _sent, _ = _make_channel(initial_state=ConnectionState.DISCONNECTED, queue_size=3)
    channel.join()
    for i in range(3):
        channel.send_message(f"e{i}", {"i": i})
    with pytest.raises(ChannelQueueFullError):
        channel.send_message("overflow", {"i": 3})
    assert len(channel._queue) == 3  # type: ignore[attr-defined]
    assert [msg.event for msg in channel._queue] == ["e0", "e1", "e2"]  # type: ignore[attr-defined]


async def test_outbound_messages_are_published_one_at_a_time_in_fifo_order() -> None:
    state: BehaviorSubject[ConnectionState] = BehaviorSubject(ConnectionState.CONNECTED)
    sent: list[int] = []
    active = 0
    peak_active = 0

    async def publisher(msg: ClientMessage) -> None:
        nonlocal active, peak_active
        active += 1
        peak_active = max(peak_active, active)
        try:
            if msg.action == ClientActions.BROADCAST:
                value = int(msg.payload["i"])
                await asyncio.sleep((20 - value) / 10_000)
                sent.append(value)
        finally:
            active -= 1

    channel = Channel(
        name="/chat/ordered",
        params={},
        publisher=publisher,
        connection_state=state,
        max_queue_size=32,
    )
    channel.join()
    channel.handle_event(
        _server_msg(
            "/chat/ordered",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
        )
    )
    for i in range(20):
        channel.send_message("ordered", {"i": i})

    for _ in range(100):
        if len(sent) == 20:
            break
        await asyncio.sleep(0.005)

    assert sent == list(range(20))
    assert peak_active == 1


async def test_reconnect_rejoins_before_flushing_messages_in_fifo_order() -> None:
    channel, sent, state = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    state.publish(ConnectionState.DISCONNECTED)
    channel.send_message("first", {"i": 1})
    channel.send_message("second", {"i": 2})
    sent.clear()

    state.publish(ConnectionState.CONNECTED)
    await asyncio.sleep(0)
    assert [msg.action for msg in sent] == [ClientActions.JOIN_CHANNEL]

    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
            request_id=sent[0].request_id,
        )
    )
    await asyncio.sleep(0)
    await asyncio.sleep(0)
    assert [msg.event for msg in sent] == [
        ClientActions.JOIN_CHANNEL.value,
        "first",
        "second",
    ]


@pytest.mark.parametrize(
    "stale_event",
    [Events.ACKNOWLEDGE.value, Events.UNAUTHORIZED.value, Events.NOT_FOUND.value],
)
async def test_stale_join_response_cannot_complete_or_decline_rejoin(
    stale_event: str,
) -> None:
    channel, sent, state = _make_channel()
    channel.join()
    await asyncio.sleep(0)
    first_join_id = sent[-1].request_id
    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
            request_id=first_join_id,
        )
    )
    state.publish(ConnectionState.DISCONNECTED)
    channel.send_message("queued", {})
    sent.clear()
    state.publish(ConnectionState.CONNECTED)
    await asyncio.sleep(0)
    current_join_id = sent[-1].request_id
    assert current_join_id != first_join_id

    channel.handle_event(
        _server_msg(
            "/chat/1",
            stale_event,
            action=ServerActions.SYSTEM,
            request_id=first_join_id,
        )
    )
    await asyncio.sleep(0)
    assert channel.channel_state == ChannelState.STALLED
    assert all(msg.event != "queued" for msg in sent)

    channel.handle_event(
        _server_msg(
            "/chat/1",
            Events.ACKNOWLEDGE.value,
            action=ServerActions.SYSTEM,
            request_id=current_join_id,
        )
    )
    await asyncio.sleep(0)
    assert channel.channel_state == ChannelState.JOINED
    assert sent[-1].event == "queued"


async def test_failed_publish_is_retained_and_retried_before_later_messages() -> None:
    state: BehaviorSubject[ConnectionState] = BehaviorSubject(ConnectionState.CONNECTED)
    attempts: list[str] = []
    delivered: list[str] = []
    fail_first = True

    async def publisher(msg: ClientMessage) -> None:
        nonlocal fail_first
        if msg.action == ClientActions.JOIN_CHANNEL:
            return
        attempts.append(msg.event)
        if fail_first:
            fail_first = False
            raise ConnectionError("simulated send failure")
        delivered.append(msg.event)

    channel = Channel(
        name="/chat/retry",
        params={},
        publisher=publisher,
        connection_state=state,
        max_queue_size=3,
    )
    channel.join()
    channel.handle_event(
        _server_msg("/chat/retry", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    channel.send_message("first", {})
    await asyncio.sleep(0)
    await asyncio.sleep(0)
    channel.send_message("second", {})
    await asyncio.sleep(0)
    await asyncio.sleep(0)

    assert attempts == ["first", "first", "second"]
    assert delivered == ["first", "second"]


async def test_timed_out_queued_request_is_not_sent_after_reconnect() -> None:
    channel, sent, state = _make_channel(initial_state=ConnectionState.DISCONNECTED)
    channel.join()
    with pytest.raises(ResponseTimeoutError):
        await channel.send_for_response("expires", {}, wait=0.01)
    assert len(channel._queue) == 0  # type: ignore[attr-defined]

    state.publish(ConnectionState.CONNECTED)
    await asyncio.sleep(0)
    channel.handle_event(
        _server_msg("/chat/1", Events.ACKNOWLEDGE.value, action=ServerActions.SYSTEM)
    )
    await asyncio.sleep(0)
    assert all(msg.event != "expires" for msg in sent)


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
