from __future__ import annotations

import asyncio

from pondsocket import (
    ConnectionContext,
    EventContext,
    JoinContext,
    PondSocket,
    SystemEvents,
)
from pondsocket.types import Event
from pondsocket_asgi import PondASGIApp
from pondsocket_asgi._sse import event_to_sse_frame
from pondsocket_asgi.testing import ASGITestDriver
from pondsocket_common import ClientActions, ServerActions


def _driver_for(pond: PondSocket) -> ASGITestDriver:
    return ASGITestDriver(PondASGIApp(pond))


def test_event_to_sse_frame_format() -> None:
    ev = Event(
        action="BROADCAST",
        channel_name="room/1",
        request_id="r-1",
        event="chat",
        payload={"text": "hi"},
    )
    frame = event_to_sse_frame(ev)
    text = frame.decode("utf-8")
    assert text.startswith("event: chat\n")
    assert "data: " in text
    assert text.endswith("\n\n")
    data_line = next(
        line for line in text.split("\n") if line.startswith("data: ")
    )
    payload_text = data_line[len("data: "):]
    assert '"channelName":"room/1"' in payload_text
    assert '"event":"chat"' in payload_text


async def test_sse_init_unknown_path_returns_404() -> None:
    pond = PondSocket()
    sess = await _driver_for(pond).connect_sse("/missing")
    assert sess.accepted is False
    assert sess.status == 404


async def test_sse_init_accepted_returns_x_connection_id_header() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept(role="user")

    pond.create_endpoint("/sse", auth)

    sess = await _driver_for(pond).connect_sse("/sse")
    assert sess.accepted is True
    assert sess.status == 200
    assert sess.connection_id != ""

    connect_event = await sess.receive_event_named(SystemEvents.CONNECTION.value)
    assert connect_event["action"] == ServerActions.CONNECT.value
    assert connect_event["payload"]["connectionId"] == sess.connection_id

    await sess.disconnect()


async def test_sse_init_decline_maps_to_http_status() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.decline(401, "bad token")

    pond.create_endpoint("/sse", auth)

    sess = await _driver_for(pond).connect_sse("/sse")
    assert sess.accepted is False
    assert sess.status == 401


async def test_sse_init_pending_reply_delivered_after_connect() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.reply("welcome", {"server": "pondsocket"})

    pond.create_endpoint("/sse", auth)

    sess = await _driver_for(pond).connect_sse("/sse")
    assert sess.accepted is True

    await sess.receive_event_named(SystemEvents.CONNECTION.value)
    welcome = await sess.receive_event()
    assert welcome["event"] == "welcome"
    assert welcome["payload"] == {"server": "pondsocket"}

    await sess.disconnect()


async def test_sse_push_unknown_connection_id_returns_404() -> None:
    pond = PondSocket()
    status, _ = await _driver_for(pond).sse_push("nonexistent", {})
    assert status == 404


async def test_sse_push_invalid_json_is_silently_dropped_with_204() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    pond.create_endpoint("/sse", auth)

    driver = _driver_for(pond)
    sess = await driver.connect_sse("/sse")
    assert sess.accepted is True

    status, _ = await driver.call_http(
        "/push",
        method="POST",
        headers=[(b"x-connection-id", sess.connection_id.encode("utf-8"))],
        body=b"{not json}",
    )
    assert status == 204
    await sess.disconnect()


async def test_sse_full_join_broadcast_flow() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/sse", auth)

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()

    lobby = endpoint.create_channel("/chat/:room_id", on_join)

    async def on_message(ctx: EventContext) -> None:
        await ctx.broadcast(ctx.event_name, ctx.get_payload())

    lobby.on_message("message", on_message)

    driver = _driver_for(pond)
    sess = await driver.connect_sse("/sse")
    assert sess.accepted is True
    await sess.receive_event_named(SystemEvents.CONNECTION.value)

    join_status, _ = await driver.sse_push(
        sess.connection_id,
        {
            "action": ClientActions.JOIN_CHANNEL.value,
            "event": "join",
            "channelName": "/chat/1",
            "payload": {},
            "requestId": "req-join",
        },
    )
    assert join_status == 204

    ack = await sess.receive_event_named(SystemEvents.ACKNOWLEDGE.value)
    assert ack["requestId"] == "req-join"

    bc_status, _ = await driver.sse_push(
        sess.connection_id,
        {
            "action": ClientActions.BROADCAST.value,
            "event": "message",
            "channelName": "/chat/1",
            "payload": {"text": "hi"},
            "requestId": "req-bc",
        },
    )
    assert bc_status == 204

    bc = await sess.receive_event_named("message")
    assert bc["payload"] == {"text": "hi"}
    assert bc["action"] == ServerActions.BROADCAST.value

    await sess.disconnect()


async def test_sse_disconnect_via_http_delete_closes_transport() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/sse", auth)

    driver = _driver_for(pond)
    sess = await driver.connect_sse("/sse")
    assert sess.accepted is True
    await sess.receive_event_named(SystemEvents.CONNECTION.value)

    status, _ = await driver.sse_disconnect(sess.connection_id)
    assert status == 204

    for _ in range(50):
        if await endpoint.connection_count() == 0:
            break
        await asyncio.sleep(0.01)
    assert await endpoint.connection_count() == 0
    await sess.disconnect()


async def test_sse_disconnect_unknown_id_returns_404() -> None:
    pond = PondSocket()
    status, _ = await _driver_for(pond).sse_disconnect("missing")
    assert status == 404


async def test_sse_http_disconnect_event_closes_transport() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/sse", auth)

    driver = _driver_for(pond)
    sess = await driver.connect_sse("/sse")
    assert sess.accepted is True
    await sess.receive_event_named(SystemEvents.CONNECTION.value)

    await sess.disconnect()

    for _ in range(50):
        if await endpoint.connection_count() == 0:
            break
        await asyncio.sleep(0.01)
    assert await endpoint.connection_count() == 0
