from __future__ import annotations

import asyncio
import json

from pondsocket_client import BaseClient, ClientOptions, ConnectionState

from pondsocket import (
    ConnectionContext,
    EventContext,
    JoinContext,
    PondSocket,
)
from pondsocket_asgi import PondASGIApp
from pondsocket_asgi.testing import ASGITestDriver, WebSocketSession
from pondsocket_common import ChannelState, ClientMessage


class _BridgeClient(BaseClient):
    __slots__ = ("_pump_task", "_session")

    def __init__(self, session: WebSocketSession, options: ClientOptions | None = None) -> None:
        super().__init__(options)
        self._session = session
        self._pump_task: asyncio.Task[None] | None = None

    async def _open_transport(self) -> None:
        if not self._session.accepted:
            raise ConnectionError("session was not accepted")
        self._mark_connected()
        self._pump_task = asyncio.create_task(self._pump_inbound())

    async def _close_transport(self) -> None:
        if self._pump_task is not None and not self._pump_task.done():
            self._pump_task.cancel()
        await self._session.disconnect()

    async def _send_outbound(self, message: ClientMessage) -> None:
        wire = message.model_dump(by_alias=True, mode="json")
        await self._session.send_json(wire)

    async def _pump_inbound(self) -> None:
        try:
            while True:
                msg = await self._session.receive_message(wait=5.0)
                if msg.get("type") != "websocket.send":
                    continue
                text = msg.get("text")
                if isinstance(text, str):
                    self._handle_inbound_text(text)
                elif isinstance(text, bytes):
                    self._handle_inbound_text(text.decode("utf-8"))
        except asyncio.CancelledError:
            return
        except Exception as e:
            self._errors.publish(e)
            self._mark_disconnected()


async def _build_pond() -> PondSocket:
    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    async def on_join(ctx: JoinContext) -> None:
        await ctx.accept()
        await ctx.track({"user": ctx.transport.get_id()})

    async def on_message(ctx: EventContext) -> None:
        await ctx.broadcast(ctx.event_name, ctx.get_payload())

    async def on_ping(ctx: EventContext) -> None:
        await ctx.reply("pong", {"echo": ctx.get_payload().get("text")})

    pond = PondSocket()
    endpoint = pond.create_endpoint("/ws", auth)
    lobby = endpoint.create_channel("/chat/:room_id", on_join)
    lobby.on_message("message", on_message)
    lobby.on_message("ping", on_ping)
    return pond


async def test_client_join_and_broadcast_through_real_server() -> None:
    pond = await _build_pond()
    driver = ASGITestDriver(PondASGIApp(pond))
    session = await driver.connect_websocket("/ws")
    assert session.accepted

    connect_msg = await session.receive_message(wait=1.0)
    assert json.loads(connect_msg["text"])["action"] == "CONNECT"

    client = _BridgeClient(session)
    await client.connect()
    channel = client.create_channel("/chat/1", {})
    received: list[str] = []
    channel.on_message_event("message", lambda m: received.append(m.event))

    channel.join()
    for _ in range(50):
        if channel.channel_state == ChannelState.JOINED:
            break
        await asyncio.sleep(0.02)
    assert channel.channel_state == ChannelState.JOINED

    channel.send_message("message", {"text": "hello"})
    for _ in range(50):
        if received:
            break
        await asyncio.sleep(0.02)
    assert received == ["message"]

    await client.disconnect()
    await pond.close()


async def test_client_send_for_response_round_trip() -> None:
    pond = await _build_pond()
    driver = ASGITestDriver(PondASGIApp(pond))
    session = await driver.connect_websocket("/ws")
    assert session.accepted
    await session.receive_message(wait=1.0)

    client = _BridgeClient(session)
    await client.connect()
    channel = client.create_channel("/chat/1", {})
    channel.join()
    for _ in range(50):
        if channel.channel_state == ChannelState.JOINED:
            break
        await asyncio.sleep(0.02)

    result = await channel.send_for_response("ping", {"text": "abc"}, wait=2.0)
    assert result == {"echo": "abc"}

    await client.disconnect()
    await pond.close()


async def test_client_disconnect_releases_resources() -> None:
    pond = await _build_pond()
    driver = ASGITestDriver(PondASGIApp(pond))
    session = await driver.connect_websocket("/ws")
    await session.receive_message(wait=1.0)

    client = _BridgeClient(session)
    await client.connect()
    assert client.get_state() == ConnectionState.CONNECTED

    await client.disconnect()
    assert client.get_state() == ConnectionState.DISCONNECTED
    await pond.close()
