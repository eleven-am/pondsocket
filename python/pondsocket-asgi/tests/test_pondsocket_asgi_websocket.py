from __future__ import annotations

import asyncio
import json
from typing import Any

from pondsocket import (
    ConnectionContext,
    EventContext,
    JoinContext,
    LeaveContext,
    PondSocket,
    SystemEvents,
)
from pondsocket_asgi import PondASGIApp
from pondsocket_asgi.testing import ASGITestDriver, WebSocketSession
from pondsocket_common import (
    ClientActions,
    ErrorTypes,
    PresenceEventTypes,
    ServerActions,
)


def _driver_for_pond(pond: PondSocket) -> ASGITestDriver:
    return ASGITestDriver(PondASGIApp(pond))


async def _expect_action_event(
    sess: WebSocketSession,
    action: str,
    event_name: str,
    *,
    wait: float = 1.0,
) -> dict[str, Any]:
    for _ in range(30):
        msg = await sess.receive_json(wait=wait)
        if msg.get("action") == action and msg.get("event") == event_name:
            return msg
    raise AssertionError(f"did not receive {action}/{event_name}")


async def test_unknown_path_rejected_with_4404() -> None:
    pond = PondSocket()
    sess = await _driver_for_pond(pond).connect_websocket("/totally/missing")
    assert sess.accepted is False
    assert sess.closed is True
    close_msg = await sess.outbox.get()
    assert close_msg["type"] == "websocket.close"
    assert close_msg["code"] == 4404


async def test_connection_decline_maps_http_code_to_ws_close() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.decline(403, "no entry")

    pond.create_endpoint("/ws", auth)

    sess = await _driver_for_pond(pond).connect_websocket("/ws")
    assert sess.accepted is False
    close_msg = await sess.outbox.get()
    assert close_msg["type"] == "websocket.close"
    assert close_msg["code"] == 4403
    assert close_msg["reason"] == "no entry"


async def test_connection_accept_sends_websocket_accept_then_connect_event() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept(role="user")

    pond.create_endpoint("/ws", auth)

    sess = await _driver_for_pond(pond).connect_websocket("/ws")
    assert sess.accepted is True

    connect = await sess.receive_json()
    assert connect["action"] == ServerActions.CONNECT.value
    assert connect["event"] == SystemEvents.CONNECTION.value
    assert connect["payload"]["connectionId"] == connect["payload"]["id"]
    await sess.disconnect()


async def test_connection_reply_delivered_after_accept() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.reply("welcome", {"server": "pondsocket"})

    pond.create_endpoint("/ws", auth)

    sess = await _driver_for_pond(pond).connect_websocket("/ws")
    assert sess.accepted is True

    await sess.receive_json()
    welcome = await sess.receive_json()
    assert welcome["action"] == ServerActions.SYSTEM.value
    assert welcome["event"] == "welcome"
    assert welcome["payload"] == {"server": "pondsocket"}
    await sess.disconnect()


async def test_full_join_broadcast_flow_end_to_end() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/ws", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_message(ctx: EventContext) -> None:
        await ctx.broadcast(ctx.event_name, ctx.get_payload())

    lobby.on_message("message", on_message)

    driver = _driver_for_pond(pond)
    alice = await driver.connect_websocket("/ws")
    bob = await driver.connect_websocket("/ws")
    await alice.receive_json()
    await bob.receive_json()

    rid_a = await alice.send_client_message(
        action=ClientActions.JOIN_CHANNEL.value,
        channel_name="/chat/1",
        event="join",
    )
    ack_a = await _expect_action_event(
        alice, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value
    )
    assert ack_a["requestId"] == rid_a

    await bob.send_client_message(
        action=ClientActions.JOIN_CHANNEL.value,
        channel_name="/chat/1",
        event="join",
    )
    await _expect_action_event(bob, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)

    await alice.send_client_message(
        action=ClientActions.BROADCAST.value,
        channel_name="/chat/1",
        event="message",
        payload={"text": "hello"},
    )

    msg_a = await _expect_action_event(alice, ServerActions.BROADCAST.value, "message")
    msg_b = await _expect_action_event(bob, ServerActions.BROADCAST.value, "message")
    assert msg_a["payload"] == {"text": "hello"}
    assert msg_b["payload"] == {"text": "hello"}

    await alice.disconnect()
    await bob.disconnect()


async def test_invalid_inbound_json_replies_with_invalid_message_error() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    pond.create_endpoint("/ws", auth)

    sess = await _driver_for_pond(pond).connect_websocket("/ws")
    await sess.receive_json()

    await sess.send_text("{not valid json}")

    err = await _expect_action_event(
        sess, ServerActions.ERROR.value, ErrorTypes.INVALID_MESSAGE.value
    )
    assert "error" in err["payload"]
    await sess.disconnect()


async def test_disconnect_fires_leave_handler() -> None:
    pond = PondSocket()
    leaves: list[tuple[str, str]] = []

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/ws", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_leave(ctx: LeaveContext) -> None:
        leaves.append((ctx.user.id, ctx.reason))

    lobby.on_leave(on_leave)

    sess = await _driver_for_pond(pond).connect_websocket("/ws")
    connect = await sess.receive_json()
    user_id = connect["payload"]["id"]

    await sess.send_client_message(
        action=ClientActions.JOIN_CHANNEL.value,
        channel_name="/chat/1",
        event="join",
    )
    await _expect_action_event(sess, ServerActions.SYSTEM.value, SystemEvents.ACKNOWLEDGE.value)

    await sess.disconnect()

    for _ in range(50):
        if leaves:
            break
        await asyncio.sleep(0.01)
    assert leaves == [(user_id, "connection_closed")]


async def test_presence_join_event_received_over_the_wire() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/ws", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()
        await ctx.track({"status": "online"})

    endpoint.create_channel("/chat/:room_id", on_join)

    sess = await _driver_for_pond(pond).connect_websocket("/ws")
    await sess.receive_json()

    await sess.send_client_message(
        action=ClientActions.JOIN_CHANNEL.value,
        channel_name="/chat/1",
        event="join",
    )

    presence = await _expect_action_event(
        sess, ServerActions.PRESENCE.value, PresenceEventTypes.JOIN.value
    )
    assert presence["payload"]["changed"] == {"status": "online"}
    assert presence["payload"]["presence"] == [{"status": "online"}]
    await sess.disconnect()


async def test_query_string_and_headers_visible_to_connection_handler() -> None:
    pond = PondSocket()
    captured: dict[str, Any] = {}

    async def auth(ctx: ConnectionContext) -> None:
        captured["token"] = ctx.query.get("token")
        captured["header"] = ctx.headers.get("x-custom")
        captured["address"] = ctx.address
        ctx.accept()

    pond.create_endpoint("/ws", auth)

    driver = _driver_for_pond(pond)
    sess = await driver.connect_websocket(
        "/ws",
        query_string=b"token=secret&debug=1",
        headers=[(b"x-custom", b"yes")],
    )
    await sess.receive_json()

    assert captured == {
        "token": "secret",
        "header": "yes",
        "address": "127.0.0.1",
    }
    await sess.disconnect()


async def test_path_params_propagate_to_endpoint_route() -> None:
    pond = PondSocket()
    captured: dict[str, Any] = {}

    async def auth(ctx: ConnectionContext) -> None:
        captured["params"] = dict(ctx.params)
        ctx.accept()

    pond.create_endpoint("/ws/:version", auth)

    sess = await _driver_for_pond(pond).connect_websocket("/ws/v3")
    await sess.receive_json()
    assert captured["params"] == {"version": "v3"}
    await sess.disconnect()


async def test_invalid_inbound_message_with_wrong_action_returns_error() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    pond.create_endpoint("/ws", auth)

    sess = await _driver_for_pond(pond).connect_websocket("/ws")
    await sess.receive_json()

    await sess.send_text(
        json.dumps(
            {
                "action": "BOGUS_ACTION",
                "event": "x",
                "channelName": "/chat/1",
                "requestId": "r",
                "payload": {},
            }
        )
    )

    err = await _expect_action_event(
        sess, ServerActions.ERROR.value, ErrorTypes.INVALID_MESSAGE.value
    )
    assert err["channelName"] == "GATEWAY"
    await sess.disconnect()
