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


async def test_disconnect_wins_against_cancellation_resistant_delayed_open() -> None:
    class _DelayedClient(_FakeClient):
        __slots__ = ("allow_open", "close_count", "open_started", "resource_open")

        def __init__(self) -> None:
            super().__init__()
            self.allow_open = asyncio.Event()
            self.close_count = 0
            self.open_started = asyncio.Event()
            self.resource_open = False

        async def _open_transport(self) -> None:
            self.open_started.set()
            try:
                await self.allow_open.wait()
            except asyncio.CancelledError:
                await self.allow_open.wait()
            self.resource_open = True
            self._mark_connected()

        async def _close_transport(self) -> None:
            self.close_count += 1
            self.resource_open = False

    client = _DelayedClient()
    states: list[ConnectionState] = []
    client.on_connection_change(states.append)
    connect_task = asyncio.create_task(client.connect())
    await client.open_started.wait()
    disconnect_task = asyncio.create_task(client.disconnect())
    await asyncio.sleep(0)
    client.allow_open.set()

    await asyncio.wait_for(disconnect_task, timeout=1.0)
    await asyncio.wait_for(connect_task, timeout=1.0)

    assert client.get_state() == ConnectionState.DISCONNECTED
    assert not client.resource_open
    assert client.close_count >= 1
    assert states[-1] == ConnectionState.DISCONNECTED


async def test_create_channel_is_idempotent() -> None:
    client = _FakeClient()
    a = client.create_channel("/chat/1", {})
    b = client.create_channel("/chat/1", {})
    assert a is b


async def test_create_channel_does_not_replace_one_still_flushing_leave() -> None:
    class _LeaveBlockingClient(_FakeClient):
        __slots__ = ("allow_leave",)

        def __init__(self) -> None:
            super().__init__()
            self.allow_leave = asyncio.Event()

        async def _send_outbound(self, message: ClientMessage) -> None:
            if message.action.value == "LEAVE_CHANNEL":
                await self.allow_leave.wait()
            self.sent.append(message)

    client = _LeaveBlockingClient()
    await client.connect()
    channel = client.create_channel("/chat/1", {})
    channel.join()
    await asyncio.sleep(0)
    client._handle_inbound_text(
        '{"action":"SYSTEM","event":"ACKNOWLEDGE","channelName":"/chat/1",'
        '"requestId":"r1","payload":{}}'
    )
    channel.leave()
    await asyncio.sleep(0)

    assert client.create_channel("/chat/1", {}) is channel
    client.allow_leave.set()
    await asyncio.sleep(0)
    replacement = client.create_channel("/chat/1", {})
    assert replacement is not channel
    await client.disconnect()


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


async def test_newline_batched_inbound_frames_are_dispatched_in_order() -> None:
    client = _FakeClient()
    await client.connect()
    channel = client.create_channel("/chat/42", {})
    channel.join()
    received: list[str] = []
    channel.on_message(lambda msg: received.append(msg.event))

    client._handle_inbound_text(
        '{"action":"SYSTEM","event":"ACKNOWLEDGE","channelName":"/chat/42",'
        '"requestId":"join","payload":{}}\n'
        '{"action":"BROADCAST","event":"first","channelName":"/chat/42",'
        '"requestId":"r1","payload":{}}\n'
        '{"action":"BROADCAST","event":"second","channelName":"/chat/42",'
        '"requestId":"r2","payload":{}}\n'
    )

    assert received == ["first", "second"]
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


async def test_transport_sends_are_serialized_across_channels() -> None:
    class _SlowClient(_FakeClient):
        __slots__ = ("active_sends", "peak_active_sends")

        def __init__(self) -> None:
            super().__init__()
            self.active_sends = 0
            self.peak_active_sends = 0

        async def _send_outbound(self, message: ClientMessage) -> None:
            self.active_sends += 1
            self.peak_active_sends = max(self.peak_active_sends, self.active_sends)
            try:
                await asyncio.sleep(0.001)
                self.sent.append(message)
            finally:
                self.active_sends -= 1

    client = _SlowClient()
    await client.connect()
    first = client.create_channel("/first", {})
    second = client.create_channel("/second", {})
    first.join()
    second.join()
    await asyncio.sleep(0.01)
    client._handle_inbound_text(
        '{"action":"SYSTEM","event":"ACKNOWLEDGE","channelName":"/first",'
        '"requestId":"r1","payload":{}}'
    )
    client._handle_inbound_text(
        '{"action":"SYSTEM","event":"ACKNOWLEDGE","channelName":"/second",'
        '"requestId":"r2","payload":{}}'
    )
    first.send_message("one", {})
    second.send_message("two", {})
    first.send_message("three", {})
    await asyncio.sleep(0.02)

    assert client.peak_active_sends == 1
    assert [msg.event for msg in client.sent if msg.action.value == "BROADCAST"] == [
        "one",
        "two",
        "three",
    ]
    await client.disconnect()


async def test_disconnect_cancels_an_inflight_channel_send() -> None:
    class _BlockingClient(_FakeClient):
        __slots__ = ("send_cancelled", "send_started")

        def __init__(self) -> None:
            super().__init__()
            self.send_cancelled = False
            self.send_started = asyncio.Event()

        async def _send_outbound(self, message: ClientMessage) -> None:
            self.send_started.set()
            try:
                await asyncio.Event().wait()
            except asyncio.CancelledError:
                self.send_cancelled = True
                raise

    client = _BlockingClient()
    await client.connect()
    channel = client.create_channel("/blocked", {})
    channel.join()
    await asyncio.wait_for(client.send_started.wait(), timeout=1.0)

    await asyncio.wait_for(client.disconnect(), timeout=1.0)

    assert client.send_cancelled
    assert client.get_state() == ConnectionState.DISCONNECTED
