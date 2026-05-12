from __future__ import annotations

import asyncio

import pytest

from pondsocket.errors import PondError
from pondsocket.in_memory_transport import InMemoryTransport
from pondsocket.transport import Transport
from pondsocket.types import Event, TransportType


def _event(name: str = "ping") -> Event:
    return Event(action="BROADCAST", channel_name="room/1", request_id="r1", event=name)


def test_in_memory_transport_satisfies_protocol() -> None:
    t = InMemoryTransport("u1")
    assert isinstance(t, Transport)


async def test_id_and_initial_state() -> None:
    t = InMemoryTransport("u1", assigns={"role": "admin"})
    assert t.get_id() == "u1"
    assert t.is_active() is True
    assert t.get_assign("role") == "admin"
    assert t.transport_type() is TransportType.WEBSOCKET


async def test_assigns_set_get_clone_returns_independent_copy() -> None:
    t = InMemoryTransport("u1", assigns={"a": 1})
    t.set_assign("b", 2)
    assert t.clone_assigns() == {"a": 1, "b": 2}
    snap = t.clone_assigns()
    snap["c"] = 3
    assert t.get_assign("c") is None


async def test_send_event_puts_in_outbox() -> None:
    t = InMemoryTransport("u1")
    ev = _event("hello")
    await t.send_event(ev)
    received = await t.receive_sent()
    assert received is ev


async def test_send_event_after_close_raises_pond_error() -> None:
    t = InMemoryTransport("u1")
    await t.close()
    with pytest.raises(PondError):
        await t.send_event(_event())


async def test_close_fires_callbacks_in_registration_order() -> None:
    t = InMemoryTransport("u1")
    fired: list[str] = []

    async def cb_a(_t: Transport) -> None:
        fired.append("a")

    async def cb_b(_t: Transport) -> None:
        fired.append("b")

    t.on_close(cb_a)
    t.on_close(cb_b)
    await t.close()
    assert fired == ["a", "b"]


async def test_close_is_idempotent() -> None:
    t = InMemoryTransport("u1")
    fired: list[str] = []

    async def cb(_t: Transport) -> None:
        fired.append("x")

    t.on_close(cb)
    await t.close()
    await t.close()
    assert fired == ["x"]


async def test_close_callback_exceptions_do_not_stop_other_callbacks() -> None:
    t = InMemoryTransport("u1")
    fired: list[str] = []

    async def good(_t: Transport) -> None:
        fired.append("good")

    async def bad(_t: Transport) -> None:
        raise RuntimeError("boom")

    t.on_close(bad)
    t.on_close(good)
    await t.close()
    assert fired == ["good"]


async def test_handle_messages_requires_on_message() -> None:
    t = InMemoryTransport("u1")
    with pytest.raises(RuntimeError, match="on_message must be set"):
        await t.handle_messages()


async def test_handle_messages_dispatches_inbound_to_handler() -> None:
    t = InMemoryTransport("u1")
    received: list[Event] = []

    async def handler(ev: Event, _t: Transport) -> None:
        received.append(ev)

    t.on_message(handler)
    await t.handle_messages()

    ev = _event("incoming")
    await t.push_inbound(ev)

    for _ in range(50):
        if received:
            break
        await asyncio.sleep(0.005)

    assert received == [ev]
    await t.close()


async def test_handler_exception_does_not_kill_loop() -> None:
    t = InMemoryTransport("u1")
    seen: list[str] = []

    async def handler(ev: Event, _t: Transport) -> None:
        if ev.event == "bad":
            raise RuntimeError("boom")
        seen.append(ev.event)

    t.on_message(handler)
    await t.handle_messages()

    await t.push_inbound(_event("bad"))
    await t.push_inbound(_event("good"))

    for _ in range(50):
        if "good" in seen:
            break
        await asyncio.sleep(0.005)

    assert seen == ["good"]
    await t.close()


async def test_in_memory_transport_is_not_pushable() -> None:
    from pondsocket.transport import PushableTransport

    t = InMemoryTransport("u1")
    assert not isinstance(t, PushableTransport)


async def test_drain_and_outbox_size() -> None:
    t = InMemoryTransport("u1")
    await t.send_event(_event("a"))
    await t.send_event(_event("b"))
    assert t.outbox_size() == 2
    events = await t.drain_outbox()
    assert [e.event for e in events] == ["a", "b"]
    assert t.outbox_size() == 0


async def test_handle_messages_called_twice_is_safe() -> None:
    t = InMemoryTransport("u1")

    async def handler(_e: Event, _t: Transport) -> None:
        pass

    t.on_message(handler)
    await t.handle_messages()
    await t.handle_messages()
    await t.close()
