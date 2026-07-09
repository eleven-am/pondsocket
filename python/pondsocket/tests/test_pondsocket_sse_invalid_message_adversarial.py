from __future__ import annotations

import pytest

from pondsocket.sse_transport import SSEServerTransport
from pondsocket.types import Event


async def _capture(data: bytes) -> tuple[list[Event], Event | None]:
    t = SSEServerTransport(id="u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: object) -> None:
        received.append(ev)

    t.on_message(handler)
    await t.push_message(data)
    frame = await t.next_event() if not t._outbound.empty() else None  # type: ignore[attr-defined]
    return received, frame


async def test_empty_string_emits_invalid_message() -> None:
    received, frame = await _capture(b"")
    assert received == []
    assert frame is not None
    assert frame.action == "ERROR"
    assert frame.event == "INVALID_MESSAGE"
    assert frame.channel_name == "GATEWAY"
    assert set(frame.payload.keys()) == {"error"}


async def test_whitespace_only_emits_invalid_message() -> None:
    received, frame = await _capture(b"   \n\t ")
    assert received == []
    assert frame is not None
    assert frame.event == "INVALID_MESSAGE"


@pytest.mark.parametrize("body", [b"123", b"null", b"true", b'"a string"', b"[]", b"[1,2,3]"])
async def test_valid_json_that_is_not_an_object_emits_invalid_message(body: bytes) -> None:
    received, frame = await _capture(body)
    assert received == []
    assert frame is not None
    assert frame.action == "ERROR"
    assert frame.event == "INVALID_MESSAGE"
    assert frame.channel_name == "GATEWAY"


async def test_invalid_message_frame_has_fresh_request_id() -> None:
    _, frame = await _capture(b"{not json}")
    assert frame is not None
    assert isinstance(frame.request_id, str)
    assert frame.request_id != ""


async def test_error_path_does_not_raise_when_transport_closed() -> None:
    t = SSEServerTransport(id="u1")

    async def handler(ev: Event, _t: object) -> None:
        return

    t.on_message(handler)
    await t.close()
    await t.push_message(b"{not json}")
    assert t._outbound.empty()  # type: ignore[attr-defined]


async def test_sse_invalid_frame_matches_asgi_invalid_frame_shape() -> None:
    import json

    from pondsocket_asgi.transport import ASGIWebSocketTransport

    _, sse_frame = await _capture(b"{not json}")
    assert sse_frame is not None

    outbound: list[dict] = []

    async def send(m: dict) -> None:
        outbound.append(m)

    async def receive() -> dict:
        return {"type": "websocket.disconnect"}

    asgi = ASGIWebSocketTransport(id="u1", send=send, receive=receive)

    async def handler(ev: Event, _t: object) -> None:
        return

    asgi.on_message(handler)
    await asgi._dispatch_receive({"type": "websocket.receive", "text": "{not json}"})  # type: ignore[attr-defined]
    assert len(outbound) == 1
    asgi_wire = json.loads(outbound[0]["text"])

    assert sse_frame.action == asgi_wire["action"] == "ERROR"
    assert sse_frame.channel_name == asgi_wire["channelName"] == "GATEWAY"
    assert sse_frame.event == asgi_wire["event"] == "INVALID_MESSAGE"
    assert set(sse_frame.payload.keys()) == set(asgi_wire["payload"].keys()) == {"error"}


async def test_deeply_nested_json_emits_invalid_message_instead_of_raising() -> None:
    t = SSEServerTransport(id="u1")

    async def handler(ev: Event, _t: object) -> None:
        return

    t.on_message(handler)
    data = (b"[" * 50000) + (b"]" * 50000)
    await t.push_message(data)
    assert not t._outbound.empty()  # type: ignore[attr-defined]
    frame = await t.next_event()
    assert frame is not None
    assert frame.event == "INVALID_MESSAGE"
