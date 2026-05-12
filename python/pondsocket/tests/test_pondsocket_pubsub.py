from __future__ import annotations

import asyncio

import pytest

from pondsocket.errors import PondError
from pondsocket.pubsub import (
    PubSub,
    PubSubClosedError,
    format_message_topic,
    format_presence_topic,
    format_system_topic,
    format_topic,
    match_topic,
)
from pondsocket.pubsub_local import LocalPubSub


def test_match_topic_exact() -> None:
    assert match_topic("a.b", "a.b") is True
    assert match_topic("a.b", "a.c") is False


def test_match_topic_wildcard_suffix() -> None:
    assert match_topic("pondsocket:api:room:.*", "pondsocket:api:room:message") is True
    assert match_topic("pondsocket:api:room:.*", "pondsocket:api:room:presence") is True
    assert match_topic("pondsocket:api:room:.*", "pondsocket:api:lobby:msg") is False


def test_match_topic_too_short_pattern_rejects_unless_exact() -> None:
    assert match_topic(".*", "anything") is False


def test_match_topic_minimal_wildcard_pattern_matches_prefix() -> None:
    assert match_topic("a.*", "ab") is True
    assert match_topic("a.*", "ba") is False


def test_format_helpers() -> None:
    assert format_topic("api", "room", "message") == "pondsocket:api:room:message"
    assert format_presence_topic("api", "room") == "pondsocket:api:room:presence"
    assert format_message_topic("api", "room") == "pondsocket:api:room:message"
    assert format_system_topic("ping") == "pondsocket:system:ping"


def test_local_pubsub_satisfies_protocol() -> None:
    bus = LocalPubSub()
    assert isinstance(bus, PubSub)


async def test_subscribe_and_publish_delivers_to_handler() -> None:
    bus = LocalPubSub()
    received: list[tuple[str, bytes]] = []

    async def handler(topic: str, data: bytes) -> None:
        received.append((topic, data))

    await bus.subscribe("pondsocket:api:room:.*", handler)
    await bus.publish("pondsocket:api:room:message", b'{"k":1}')

    for _ in range(50):
        if received:
            break
        await asyncio.sleep(0.005)

    assert received == [("pondsocket:api:room:message", b'{"k":1}')]
    await bus.close()


async def test_publish_to_no_matching_pattern_is_silent() -> None:
    bus = LocalPubSub()
    seen: list[str] = []

    async def handler(topic: str, _data: bytes) -> None:
        seen.append(topic)

    await bus.subscribe("pondsocket:api:room:.*", handler)
    await bus.publish("pondsocket:other:lobby:x", b"")
    await asyncio.sleep(0.02)
    assert seen == []
    await bus.close()


async def test_multiple_subscribers_each_receive() -> None:
    bus = LocalPubSub()
    a: list[bytes] = []
    b: list[bytes] = []

    async def ha(_topic: str, data: bytes) -> None:
        a.append(data)

    async def hb(_topic: str, data: bytes) -> None:
        b.append(data)

    await bus.subscribe("topic.*", ha)
    await bus.subscribe("topic.*", hb)
    await bus.publish("topic.x", b"hello")

    for _ in range(50):
        if a and b:
            break
        await asyncio.sleep(0.005)

    assert a == [b"hello"]
    assert b == [b"hello"]
    await bus.close()


async def test_unsubscribe_stops_delivery() -> None:
    bus = LocalPubSub()
    received: list[bytes] = []

    async def handler(_topic: str, data: bytes) -> None:
        received.append(data)

    await bus.subscribe("topic.*", handler)
    await bus.publish("topic.x", b"a")
    await asyncio.sleep(0.02)
    assert received == [b"a"]

    await bus.unsubscribe("topic.*")
    await bus.publish("topic.x", b"b")
    await asyncio.sleep(0.02)
    assert received == [b"a"]
    await bus.close()


async def test_unsubscribe_unknown_pattern_raises_not_found() -> None:
    bus = LocalPubSub()
    with pytest.raises(PondError) as exc:
        await bus.unsubscribe("missing.*")
    assert exc.value.code == 404
    await bus.close()


async def test_publish_after_close_raises() -> None:
    bus = LocalPubSub()
    await bus.close()
    with pytest.raises(PubSubClosedError):
        await bus.publish("topic.x", b"")


async def test_subscribe_after_close_raises() -> None:
    bus = LocalPubSub()
    await bus.close()

    async def handler(_t: str, _d: bytes) -> None:
        pass

    with pytest.raises(PubSubClosedError):
        await bus.subscribe("topic.*", handler)


async def test_close_is_idempotent() -> None:
    bus = LocalPubSub()
    await bus.close()
    await bus.close()


async def test_handler_exception_does_not_kill_subscription() -> None:
    bus = LocalPubSub()
    seen: list[str] = []

    async def handler(topic: str, _data: bytes) -> None:
        if topic.endswith("bad"):
            raise RuntimeError("boom")
        seen.append(topic)

    await bus.subscribe("topic.*", handler)
    await bus.publish("topic.bad", b"")
    await bus.publish("topic.good", b"")

    for _ in range(50):
        if "topic.good" in seen:
            break
        await asyncio.sleep(0.005)

    assert seen == ["topic.good"]
    await bus.close()


async def test_full_queue_drops_silently() -> None:
    bus = LocalPubSub(buffer_size=1)

    started = asyncio.Event()
    proceed = asyncio.Event()

    async def slow_handler(_t: str, _d: bytes) -> None:
        started.set()
        await proceed.wait()

    await bus.subscribe("topic.*", slow_handler)

    await bus.publish("topic.x", b"1")
    await started.wait()

    for i in range(10):
        await bus.publish("topic.x", b"")
        _ = i

    proceed.set()
    await asyncio.sleep(0.02)
    await bus.close()
