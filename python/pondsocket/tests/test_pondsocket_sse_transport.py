from __future__ import annotations

import asyncio
import json

import pytest

from pondsocket.errors import PondError
from pondsocket.sse_transport import SSEServerTransport
from pondsocket.transport import Transport
from pondsocket.types import Event, TransportType


def _event(name: str = "ping") -> Event:
    return Event(action="BROADCAST", channel_name="room/1", request_id="r1", event=name)


def test_sse_transport_satisfies_protocol() -> None:
    t = SSEServerTransport(id="u1")
    assert isinstance(t, Transport)
    assert t.transport_type() is TransportType.SSE


async def test_send_event_queues_outbound() -> None:
    t = SSEServerTransport(id="u1")
    ev = _event("hello")
    await t.send_event(ev)
    got = await t.next_event()
    assert got is ev


async def test_send_event_after_close_raises() -> None:
    t = SSEServerTransport(id="u1")
    await t.close()
    with pytest.raises(PondError):
        await t.send_event(_event())


async def test_next_event_returns_none_after_close_and_drain() -> None:
    t = SSEServerTransport(id="u1")
    await t.send_event(_event("a"))
    first = await t.next_event()
    assert first is not None
    await t.close()
    result = await asyncio.wait_for(t.next_event(), timeout=1.0)
    assert result is None


async def test_close_fires_close_callbacks_in_order() -> None:
    t = SSEServerTransport(id="u1")
    fired: list[str] = []

    async def cb1(_t: Transport) -> None:
        fired.append("a")

    async def cb2(_t: Transport) -> None:
        fired.append("b")

    t.on_close(cb1)
    t.on_close(cb2)
    await t.close()
    assert fired == ["a", "b"]


async def test_close_is_idempotent() -> None:
    t = SSEServerTransport(id="u1")
    fired: list[str] = []

    async def cb(_t: Transport) -> None:
        fired.append("x")

    t.on_close(cb)
    await t.close()
    await t.close()
    assert fired == ["x"]


async def test_push_message_dispatches_valid_json_to_handler() -> None:
    t = SSEServerTransport(id="u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: Transport) -> None:
        received.append(ev)

    t.on_message(handler)

    msg = {
        "action": "BROADCAST",
        "event": "chat",
        "channelName": "/room/1",
        "requestId": "r-1",
        "payload": {"text": "hi"},
    }
    await t.push_message(json.dumps(msg).encode("utf-8"))
    assert len(received) == 1
    assert received[0].event == "chat"
    assert received[0].payload == {"text": "hi"}


async def test_push_message_silently_drops_invalid_json() -> None:
    t = SSEServerTransport(id="u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: Transport) -> None:
        received.append(ev)

    t.on_message(handler)
    await t.push_message(b"{not json}")
    assert received == []


async def test_push_message_silently_drops_invalid_schema() -> None:
    t = SSEServerTransport(id="u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: Transport) -> None:
        received.append(ev)

    t.on_message(handler)
    await t.push_message(
        b'{"action":"BOGUS","event":"x","channelName":"c","requestId":"r","payload":{}}'
    )
    assert received == []


async def test_push_message_emits_invalid_message_frame_for_bad_json() -> None:
    t = SSEServerTransport(id="u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: Transport) -> None:
        received.append(ev)

    t.on_message(handler)
    await t.push_message(b"{not json}")
    assert received == []
    frame = await t.next_event()
    assert frame is not None
    assert frame.action == "ERROR"
    assert frame.event == "INVALID_MESSAGE"
    assert frame.channel_name == "GATEWAY"
    assert "error" in frame.payload


async def test_push_message_emits_invalid_message_frame_for_bad_schema() -> None:
    t = SSEServerTransport(id="u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: Transport) -> None:
        received.append(ev)

    t.on_message(handler)
    await t.push_message(
        b'{"action":"BOGUS","event":"x","channelName":"c","requestId":"r","payload":{}}'
    )
    assert received == []
    frame = await t.next_event()
    assert frame is not None
    assert frame.action == "ERROR"
    assert frame.event == "INVALID_MESSAGE"
    assert frame.channel_name == "GATEWAY"


async def test_push_message_emits_invalid_message_frame_for_non_utf8() -> None:
    t = SSEServerTransport(id="u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: Transport) -> None:
        received.append(ev)

    t.on_message(handler)
    await t.push_message(b"\xff\xfe\xff")
    assert received == []
    frame = await t.next_event()
    assert frame is not None
    assert frame.action == "ERROR"
    assert frame.event == "INVALID_MESSAGE"
    assert frame.payload == {"error": "Binary frame is not valid UTF-8"}


async def test_push_message_with_no_handler_is_safe() -> None:
    t = SSEServerTransport(id="u1")
    await t.push_message(
        b'{"action":"BROADCAST","event":"x","channelName":"c","requestId":"r","payload":{}}'
    )


async def test_handle_messages_is_no_op() -> None:
    t = SSEServerTransport(id="u1")
    await t.handle_messages()


async def test_assigns_set_get_clone() -> None:
    t = SSEServerTransport(id="u1", assigns={"role": "admin"})
    assert t.get_assign("role") == "admin"
    t.set_assign("muted", True)
    assert t.clone_assigns() == {"role": "admin", "muted": True}
    clone = t.clone_assigns()
    clone["leak"] = True
    assert t.get_assign("leak") is None


async def test_sse_transport_is_pushable() -> None:
    from pondsocket.transport import PushableTransport

    t = SSEServerTransport(id="u1")
    assert isinstance(t, PushableTransport)


async def test_wait_until_closed_returns_after_close() -> None:
    t = SSEServerTransport(id="u1")

    async def closer() -> None:
        await asyncio.sleep(0.01)
        await t.close()

    task = asyncio.create_task(closer())
    await asyncio.wait_for(t.wait_until_closed(), timeout=1.0)
    await task
