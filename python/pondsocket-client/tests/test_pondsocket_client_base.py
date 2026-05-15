from __future__ import annotations

import asyncio

from pondsocket_client import (
    BaseClient,
    ClientOptions,
    ConnectionState,
)

from pondsocket_common import ClientMessage


class _FakeClient(BaseClient):
    __slots__ = ("opened", "sent")

    def __init__(self, options: ClientOptions | None = None) -> None:
        super().__init__(options)
        self.opened = 0
        self.sent: list[ClientMessage] = []

    async def _open_transport(self) -> None:
        self.opened += 1
        self._mark_connected()

    async def _close_transport(self) -> None:
        return

    async def _send_outbound(self, message: ClientMessage) -> None:
        self.sent.append(message)


async def test_initial_state_disconnected() -> None:
    client = _FakeClient()
    assert client.get_state() == ConnectionState.DISCONNECTED


async def test_connect_transitions_through_connecting_to_connected() -> None:
    client = _FakeClient()
    seen: list[ConnectionState] = []
    client.on_connection_change(seen.append)
    await client.connect()
    assert seen[0] == ConnectionState.DISCONNECTED
    assert ConnectionState.CONNECTING in seen
    assert seen[-1] == ConnectionState.CONNECTED


async def test_create_channel_is_idempotent() -> None:
    client = _FakeClient()
    a = client.create_channel("/chat/1", {})
    b = client.create_channel("/chat/1", {})
    assert a is b


async def test_disconnect_stops_reconnect_and_closes_channels() -> None:
    client = _FakeClient()
    await client.connect()
    channel = client.create_channel("/chat/1", {})
    channel.join()
    await client.disconnect()
    assert client.get_state() == ConnectionState.DISCONNECTED


async def test_exponential_backoff_doubles() -> None:
    options = ClientOptions(
        base_reconnect_delay=0.01,
        max_reconnect_delay=10.0,
    )

    class _FlakyClient(_FakeClient):
        opens_called: int = 0
        should_fail: bool = True

        async def _open_transport(self) -> None:
            type(self).opens_called += 1
            if type(self).should_fail:
                raise ConnectionError("nope")
            self._mark_connected()

    client = _FlakyClient(options)
    await client.connect()
    await asyncio.sleep(0.05)
    assert _FlakyClient.opens_called >= 2
    await client.disconnect()


async def test_inbound_text_routes_to_matching_channel() -> None:
    client = _FakeClient()
    await client.connect()
    channel = client.create_channel("/chat/42", {})
    channel.join()
    await asyncio.sleep(0)
    received: list[str] = []

    def on_msg(msg) -> None:  # type: ignore[no-untyped-def]
        received.append(msg.event)

    channel.on_message(on_msg)
    client._handle_inbound_text(
        '{"action":"SYSTEM","event":"ACKNOWLEDGE","channelName":"/chat/42","requestId":"r1","payload":{}}'
    )
    client._handle_inbound_text(
        '{"action":"BROADCAST","event":"ping","channelName":"/chat/42","requestId":"r2","payload":{"text":"hi"}}'
    )
    assert received == ["ping"]
    await client.disconnect()


async def test_invalid_inbound_json_does_not_crash() -> None:
    client = _FakeClient()
    errors: list[BaseException] = []
    client.on_error(errors.append)
    client._handle_inbound_text("{not valid json}")
    assert len(errors) == 1


async def test_unknown_channel_inbound_is_dropped_silently() -> None:
    client = _FakeClient()
    await client.connect()
    client._handle_inbound_text(
        '{"action":"BROADCAST","event":"x","channelName":"/unknown","requestId":"r","payload":{}}'
    )
    await client.disconnect()


async def test_options_can_be_provided() -> None:
    opts = ClientOptions(
        base_reconnect_delay=0.5,
        max_reconnect_delay=5.0,
        max_queue_size=50,
    )
    client = _FakeClient(opts)
    assert client.options.base_reconnect_delay == 0.5
    assert client.options.max_queue_size == 50
