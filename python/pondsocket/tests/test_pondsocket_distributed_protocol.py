from __future__ import annotations

import asyncio
import json
from collections.abc import Awaitable, Callable

from pondsocket.channel import (
    Channel,
    ChannelOptions,
    _distributed_bytes_to_event,
    _event_to_distributed_bytes,
)
from pondsocket.distributed_wire import distributed_bytes, heartbeat_bytes, parse_envelope
from pondsocket.heartbeat import HeartbeatCoordinator
from pondsocket.in_memory_transport import InMemoryTransport
from pondsocket.pubsub_local import LocalPubSub
from pondsocket.types import DistributedMessageType, Event, InternalActions, Options
from pondsocket_common import ServerActions

ENDPOINT = "api/socket"
CHANNEL = "/chat/1"
NODE = "node-a"
BASE_KEYS = {
    "protocol",
    "version",
    "type",
    "messageId",
    "sourceNodeId",
    "endpointName",
    "channelName",
    "timestamp",
}


def _wire(data: bytes) -> dict:
    return json.loads(data.decode("utf-8"))


def _assert_base(wire: dict, msg_type: str) -> None:
    assert wire["protocol"] == "pondsocket.distributed"
    assert wire["version"] == 1
    assert wire["type"] == msg_type
    assert isinstance(wire["messageId"], str) and wire["messageId"]
    assert wire["sourceNodeId"] == NODE
    assert wire["channelName"] == CHANNEL
    assert isinstance(wire["timestamp"], int)


def _legacy(action: str, event: str, payload: dict, subtype: str, **kwargs) -> dict:
    ev = Event(
        action=action,
        channel_name=CHANNEL,
        request_id="req-1",
        event=event,
        payload=payload,
        node_id=NODE,
        **kwargs,
    )
    return _wire(_event_to_distributed_bytes(ev, subtype, ENDPOINT))


def _new(msg_type: str, extra: dict) -> dict:
    return _wire(distributed_bytes(msg_type, NODE, ENDPOINT, CHANNEL, extra))


def test_user_message_roundtrip() -> None:
    wire = _legacy(
        ServerActions.BROADCAST.value,
        "message",
        {"text": "hi"},
        "message",
        from_user_id="alice",
        recipient_descriptor="ALL_USERS",
    )
    _assert_base(wire, "USER_MESSAGE")
    assert wire["fromUserId"] == "alice"
    assert wire["event"] == "message"
    assert wire["payload"] == {"text": "hi"}
    assert wire["requestId"] == "req-1"
    assert wire["recipientDescriptor"] == "ALL_USERS"

    ev = _distributed_bytes_to_event(json.dumps(wire).encode("utf-8"))
    assert ev is not None
    assert ev.action == ServerActions.BROADCAST.value
    assert ev.event == "message"
    assert ev.from_user_id == "alice"
    assert ev.payload == {"text": "hi"}
    assert ev.recipient_descriptor == "ALL_USERS"


def test_recipient_descriptor_all_three_forms() -> None:
    all_users = _legacy(
        ServerActions.BROADCAST.value, "m", {}, "message",
        from_user_id="alice", recipient_descriptor="ALL_USERS",
    )
    assert all_users["recipientDescriptor"] == "ALL_USERS"

    except_sender = _legacy(
        ServerActions.BROADCAST.value, "m", {}, "message",
        from_user_id="alice", recipient_descriptor="ALL_EXCEPT_SENDER",
    )
    assert except_sender["recipientDescriptor"] == "ALL_EXCEPT_SENDER"
    ev = _distributed_bytes_to_event(json.dumps(except_sender).encode("utf-8"))
    assert ev is not None and ev.recipient_descriptor == "ALL_EXCEPT_SENDER"

    explicit = _legacy(
        ServerActions.BROADCAST.value, "m", {}, "message",
        from_user_id="alice", recipient_descriptor=["u1", "u2"],
    )
    assert explicit["recipientDescriptor"] == ["u1", "u2"]
    ev2 = _distributed_bytes_to_event(json.dumps(explicit).encode("utf-8"))
    assert ev2 is not None and ev2.recipient_descriptor == ["u1", "u2"]
    assert ev2.recipients == ["u1", "u2"]


def test_presence_update_roundtrip_has_no_event_field() -> None:
    wire = _legacy(
        ServerActions.PRESENCE.value,
        "JOIN",
        {"presence": [{"id": "alice"}], "changed": {"id": "alice"}},
        "presence:join",
        user_id="alice",
    )
    _assert_base(wire, "PRESENCE_UPDATE")
    assert wire["userId"] == "alice"
    assert wire["presence"] == {"id": "alice"}
    assert "event" not in wire
    assert "payload" not in wire
    assert set(wire) == BASE_KEYS | {"userId", "presence"}


def test_presence_removed_roundtrip_userid_only() -> None:
    wire = _legacy(
        ServerActions.PRESENCE.value,
        "LEAVE",
        {"presence": [], "changed": {"id": "alice"}},
        "presence:leave",
        user_id="alice",
    )
    _assert_base(wire, "PRESENCE_REMOVED")
    assert wire["userId"] == "alice"
    assert "event" not in wire
    assert "presence" not in wire
    assert "payload" not in wire
    assert set(wire) == BASE_KEYS | {"userId"}


def test_assigns_update_roundtrip_emits_userid_and_assigns_only() -> None:
    wire = _legacy(
        InternalActions.ASSIGNS.value,
        "assigns:update",
        {"userId": "alice", "key": "role", "value": "admin", "assigns": {"role": "admin"}},
        "assigns:update",
    )
    _assert_base(wire, "ASSIGNS_UPDATE")
    assert wire["userId"] == "alice"
    assert wire["assigns"] == {"role": "admin"}
    assert "key" not in wire
    assert "value" not in wire
    assert "payload" not in wire
    assert set(wire) - BASE_KEYS == {"userId", "assigns"}


def test_assigns_update_consumer_accepts_key_value_delta() -> None:
    wire = _new("ASSIGNS_UPDATE", {"userId": "alice", "key": "role", "value": "admin"})
    ev = _distributed_bytes_to_event(json.dumps(wire).encode("utf-8"))
    assert ev is not None
    assert ev.action == InternalActions.ASSIGNS.value
    assert ev.payload["userId"] == "alice"
    assert ev.payload["key"] == "role"
    assert ev.payload["value"] == "admin"


def test_evict_user_roundtrip() -> None:
    wire = _legacy(
        InternalActions.USER_COMMAND.value,
        "user:evict",
        {"userId": "alice", "reason": "spam"},
        "user:evict",
    )
    _assert_base(wire, "EVICT_USER")
    assert wire["userId"] == "alice"
    assert wire["reason"] == "spam"


def test_user_remove_roundtrip() -> None:
    wire = _legacy(
        InternalActions.USER_COMMAND.value,
        "user:remove",
        {"userId": "alice", "reason": "gone"},
        "user:remove",
    )
    _assert_base(wire, "USER_REMOVE")
    assert wire["userId"] == "alice"
    assert "reason" not in wire
    assert set(wire) - BASE_KEYS == {"userId"}


def test_state_request_roundtrip() -> None:
    wire = _new(DistributedMessageType.STATE_REQUEST.value, {"fromNode": NODE})
    _assert_base(wire, "STATE_REQUEST")
    assert wire["fromNode"] == NODE
    assert parse_envelope(json.dumps(wire).encode())["fromNode"] == NODE


def test_state_response_roundtrip_element_key_is_id() -> None:
    users = [{"id": "alice", "assigns": {"role": "admin"}, "presence": {"status": "online"}}]
    wire = _new(DistributedMessageType.STATE_RESPONSE.value, {"users": users})
    _assert_base(wire, "STATE_RESPONSE")
    assert wire["users"] == users
    assert set(wire["users"][0]) == {"id", "assigns", "presence"}
    assert "userId" not in wire["users"][0]


def test_user_joined_roundtrip() -> None:
    wire = _new(
        DistributedMessageType.USER_JOINED.value,
        {"userId": "alice", "presence": {"status": "online"}, "assigns": {"role": "admin"}},
    )
    _assert_base(wire, "USER_JOINED")
    assert wire["userId"] == "alice"
    assert wire["presence"] == {"status": "online"}
    assert wire["assigns"] == {"role": "admin"}


def test_user_left_roundtrip() -> None:
    wire = _new(DistributedMessageType.USER_LEFT.value, {"userId": "alice"})
    _assert_base(wire, "USER_LEFT")
    assert wire["userId"] == "alice"
    assert set(wire) == BASE_KEYS | {"userId"}


def test_user_get_request_roundtrip() -> None:
    wire = _new(
        DistributedMessageType.USER_GET_REQUEST.value,
        {"userId": "alice", "requestId": "req-1", "fromNode": NODE},
    )
    _assert_base(wire, "USER_GET_REQUEST")
    assert wire["userId"] == "alice"
    assert wire["requestId"] == "req-1"
    assert wire["fromNode"] == NODE


def test_user_get_response_roundtrip() -> None:
    wire = _new(
        DistributedMessageType.USER_GET_RESPONSE.value,
        {
            "userId": "alice",
            "requestId": "req-1",
            "assigns": {"role": "admin"},
            "presence": {"status": "online"},
        },
    )
    _assert_base(wire, "USER_GET_RESPONSE")
    assert wire["userId"] == "alice"
    assert wire["requestId"] == "req-1"
    assert wire["assigns"] == {"role": "admin"}
    assert wire["presence"] == {"status": "online"}


def test_node_heartbeat_roundtrip() -> None:
    wire = _wire(heartbeat_bytes(NODE))
    assert wire["type"] == "NODE_HEARTBEAT"
    assert wire["protocol"] == "pondsocket.distributed"
    assert wire["version"] == 1
    assert wire["sourceNodeId"] == NODE
    assert wire["nodeId"] == NODE
    assert wire["endpointName"] == "__heartbeat__"
    assert wire["channelName"] == "__heartbeat__"


def test_parse_envelope_rejects_foreign_protocol() -> None:
    assert parse_envelope(b'{"protocol":"other","version":1}') is None
    assert parse_envelope(b'{"protocol":"pondsocket.distributed","version":2}') is None
    assert parse_envelope(b"not json") is None


async def _poll(cond: Callable[[], Awaitable[bool]], *, max_wait: float = 2.0) -> None:
    loop = asyncio.get_event_loop()
    deadline = loop.time() + max_wait
    while loop.time() < deadline:
        if await cond():
            return
        await asyncio.sleep(0.02)
    raise AssertionError("condition not met within timeout")


def _channel(
    bus: LocalPubSub,
    node_id: str,
    *,
    coordinator: HeartbeatCoordinator | None = None,
    interval: float = 30.0,
    timeout: float = 90.0,
) -> Channel:
    return Channel(
        ChannelOptions(
            name=CHANNEL,
            options=Options(
                node_id=node_id,
                heartbeat_interval=interval,
                heartbeat_timeout=timeout,
            ),
            endpoint_path="/api/socket",
            pubsub=bus,
            heartbeat=coordinator,
        )
    )


async def test_assigns_propagate_across_nodes() -> None:
    bus = LocalPubSub()
    ch_a = _channel(bus, "A")
    ch_b = _channel(bus, "B")
    await ch_a.start()
    await ch_b.start()

    await ch_a.add_user(InMemoryTransport("alice", assigns={"role": "user"}))
    await ch_b.add_user(InMemoryTransport("bob"))

    async def b_knows_alice() -> bool:
        return "alice" in await ch_b.get_assigns()

    await _poll(b_knows_alice)

    await ch_a.update_assign("alice", "role", "admin")

    async def b_sees_admin() -> bool:
        assigns = await ch_b.get_assigns()
        return assigns.get("alice", {}).get("role") == "admin"

    await _poll(b_sees_admin)

    await ch_a.close()
    await ch_b.close()
    await bus.close()


async def test_received_assigns_key_value_delta_is_applied() -> None:
    bus = LocalPubSub()
    ch_a = _channel(bus, "A")
    ch_b = _channel(bus, "B")
    await ch_a.start()
    await ch_b.start()

    await ch_a.add_user(InMemoryTransport("alice", assigns={"role": "user"}))
    await ch_b.add_user(InMemoryTransport("bob"))

    async def b_knows_alice() -> bool:
        return "alice" in await ch_b.get_assigns()

    await _poll(b_knows_alice)

    delta = distributed_bytes(
        DistributedMessageType.ASSIGNS_UPDATE.value,
        "A",
        "api/socket",
        CHANNEL,
        {"userId": "alice", "key": "role", "value": "admin"},
    )
    await bus.publish(ch_b._channel_topic(), delta)

    async def b_sees_admin() -> bool:
        assigns = await ch_b.get_assigns()
        return assigns.get("alice", {}).get("role") == "admin"

    await _poll(b_sees_admin)

    await ch_a.close()
    await ch_b.close()
    await bus.close()


async def test_state_request_response_syncs_existing_users() -> None:
    bus = LocalPubSub()
    ch_a = _channel(bus, "A")
    await ch_a.start()
    await ch_a.add_user(InMemoryTransport("alice", assigns={"role": "admin"}))
    await ch_a.track_presence("alice", {"status": "online"})

    ch_b = _channel(bus, "B")
    await ch_b.start()

    async def b_synced_alice() -> bool:
        assigns = await ch_b.get_assigns()
        presence = await ch_b.get_presence()
        return (
            assigns.get("alice", {}).get("role") == "admin"
            and presence.get("alice", {}).get("status") == "online"
        )

    await _poll(b_synced_alice)

    await ch_a.close()
    await ch_b.close()
    await bus.close()


async def test_user_get_request_response_across_nodes() -> None:
    bus = LocalPubSub()
    ch_a = _channel(bus, "A")
    ch_b = _channel(bus, "B")
    await ch_a.start()
    await ch_b.start()

    await ch_a.add_user(InMemoryTransport("alice", assigns={"role": "admin"}))
    await ch_a.track_presence("alice", {"status": "online"})

    user = await ch_b.get_user("alice")
    assert user.id == "alice"
    assert user.assigns == {"role": "admin"}
    assert user.presence == {"status": "online"}

    await ch_a.close()
    await ch_b.close()
    await bus.close()


async def test_evict_user_command_crosses_nodes() -> None:
    bus = LocalPubSub()
    ch_a = _channel(bus, "A")
    ch_b = _channel(bus, "B")
    await ch_a.start()
    await ch_b.start()

    alice = InMemoryTransport("alice")
    await ch_a.add_user(alice)

    await ch_b.evict_user("alice", "removed remotely")

    async def a_dropped_alice() -> bool:
        return not await ch_a.has_user("alice")

    await _poll(a_dropped_alice)

    saw_evicted = False
    for _ in range(50):
        try:
            ev = await alice.receive_sent(wait=0.1)
        except TimeoutError:
            break
        if ev.action == ServerActions.SYSTEM.value and ev.event == "EVICTED":
            saw_evicted = True
            break
    assert saw_evicted

    await ch_a.close()
    await ch_b.close()
    await bus.close()


async def test_user_remove_command_crosses_nodes() -> None:
    bus = LocalPubSub()
    ch_a = _channel(bus, "A")
    ch_b = _channel(bus, "B")
    await ch_a.start()
    await ch_b.start()

    await ch_a.add_user(InMemoryTransport("alice"))

    await ch_b.remove_user("alice", "gone")

    async def a_dropped_alice() -> bool:
        return not await ch_a.has_user("alice")

    await _poll(a_dropped_alice)

    await ch_a.close()
    await ch_b.close()
    await bus.close()


async def test_heartbeat_stale_node_cleanup_evicts_remote_users() -> None:
    bus = LocalPubSub()
    coord_a = HeartbeatCoordinator(bus, "A", interval=0.05, timeout=0.2)
    coord_b = HeartbeatCoordinator(bus, "B", interval=0.05, timeout=0.2)
    ch_a = _channel(bus, "A", coordinator=coord_a, interval=0.05, timeout=0.2)
    ch_b = _channel(bus, "B", coordinator=coord_b, interval=0.05, timeout=0.2)
    await ch_a.start()
    await ch_b.start()

    await ch_a.add_user(InMemoryTransport("alice", assigns={"role": "user"}))

    async def b_knows_alice() -> bool:
        return "alice" in await ch_b.get_assigns()

    await _poll(b_knows_alice)

    await ch_a.close()
    await coord_a.close()

    async def b_forgot_alice() -> bool:
        return "alice" not in await ch_b.get_assigns()

    await _poll(b_forgot_alice, max_wait=3.0)

    await ch_b.close()
    await coord_b.close()
    await bus.close()
