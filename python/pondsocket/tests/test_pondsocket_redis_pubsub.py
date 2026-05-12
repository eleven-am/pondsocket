from __future__ import annotations

import asyncio

import pytest

from pondsocket.distributed import RedisPubSub
from pondsocket.errors import PondError
from pondsocket.pubsub import PubSub, PubSubClosedError

fakeredis = pytest.importorskip("fakeredis")


@pytest.fixture
async def redis_pubsub() -> RedisPubSub:
    client = fakeredis.aioredis.FakeRedis()
    return RedisPubSub(client, owns_client=True)


async def test_redis_pubsub_satisfies_protocol(redis_pubsub: RedisPubSub) -> None:
    assert isinstance(redis_pubsub, PubSub)
    await redis_pubsub.close()


async def test_subscribe_and_publish_delivers_to_handler(
    redis_pubsub: RedisPubSub,
) -> None:
    received: list[tuple[str, bytes]] = []

    async def handler(topic: str, data: bytes) -> None:
        received.append((topic, data))

    await redis_pubsub.subscribe("pondsocket:api:room:.*", handler)
    await asyncio.sleep(0.05)
    await redis_pubsub.publish("pondsocket:api:room:message", b'{"hello":"world"}')

    for _ in range(50):
        if received:
            break
        await asyncio.sleep(0.02)

    assert received == [("pondsocket:api:room:message", b'{"hello":"world"}')]
    await redis_pubsub.close()


async def test_publish_to_non_matching_pattern_is_silent(
    redis_pubsub: RedisPubSub,
) -> None:
    received: list[str] = []

    async def handler(topic: str, _data: bytes) -> None:
        received.append(topic)

    await redis_pubsub.subscribe("pondsocket:api:room:.*", handler)
    await asyncio.sleep(0.05)
    await redis_pubsub.publish("pondsocket:other:lobby:msg", b"")
    await asyncio.sleep(0.1)

    assert received == []
    await redis_pubsub.close()


async def test_unsubscribe_stops_delivery(redis_pubsub: RedisPubSub) -> None:
    received: list[bytes] = []

    async def handler(_topic: str, data: bytes) -> None:
        received.append(data)

    await redis_pubsub.subscribe("pondsocket:api:room:.*", handler)
    await asyncio.sleep(0.05)
    await redis_pubsub.publish("pondsocket:api:room:message", b"a")

    for _ in range(50):
        if received:
            break
        await asyncio.sleep(0.02)
    assert received == [b"a"]

    await redis_pubsub.unsubscribe("pondsocket:api:room:.*")
    await asyncio.sleep(0.05)
    await redis_pubsub.publish("pondsocket:api:room:message", b"b")
    await asyncio.sleep(0.1)

    assert received == [b"a"]
    await redis_pubsub.close()


async def test_duplicate_subscribe_raises_not_found_error(
    redis_pubsub: RedisPubSub,
) -> None:
    async def handler(_t: str, _d: bytes) -> None:
        pass

    await redis_pubsub.subscribe("topic.*", handler)
    with pytest.raises(PondError):
        await redis_pubsub.subscribe("topic.*", handler)
    await redis_pubsub.close()


async def test_unsubscribe_unknown_pattern_raises(redis_pubsub: RedisPubSub) -> None:
    with pytest.raises(PondError):
        await redis_pubsub.unsubscribe("missing.*")
    await redis_pubsub.close()


async def test_publish_after_close_raises() -> None:
    client = fakeredis.aioredis.FakeRedis()
    bus = RedisPubSub(client, owns_client=True)
    await bus.close()
    with pytest.raises(PubSubClosedError):
        await bus.publish("topic.x", b"")


async def test_close_is_idempotent() -> None:
    client = fakeredis.aioredis.FakeRedis()
    bus = RedisPubSub(client, owns_client=True)
    await bus.close()
    await bus.close()


def test_glob_metachars_in_channel_names_are_escaped() -> None:
    from pondsocket.distributed.redis_pubsub import _pond_pattern_to_redis

    assert _pond_pattern_to_redis("pondsocket:api:room[1]:.*") == r"pondsocket:api:room\[1]:*"
    assert _pond_pattern_to_redis("pondsocket:api:ro?m:.*") == r"pondsocket:api:ro\?m:*"
    assert _pond_pattern_to_redis("pondsocket:api:r*m:.*") == r"pondsocket:api:r\*m:*"
    assert _pond_pattern_to_redis("pondsocket:api:plain:.*") == "pondsocket:api:plain:*"


async def test_channel_name_with_glob_chars_does_not_match_unrelated_topic() -> None:
    client = fakeredis.aioredis.FakeRedis()
    bus = RedisPubSub(client, owns_client=True)

    received: list[str] = []

    async def handler(topic: str, _data: bytes) -> None:
        received.append(topic)

    await bus.subscribe("pondsocket:api:room[1]:.*", handler)
    await asyncio.sleep(0.05)
    await bus.publish("pondsocket:api:roomX:message", b"")
    await asyncio.sleep(0.1)
    assert received == []

    await bus.publish("pondsocket:api:room[1]:message", b"")
    for _ in range(50):
        if received:
            break
        await asyncio.sleep(0.02)
    assert received == ["pondsocket:api:room[1]:message"]
    await bus.close()
