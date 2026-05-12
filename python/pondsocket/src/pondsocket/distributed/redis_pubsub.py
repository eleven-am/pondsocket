from __future__ import annotations

import asyncio
import contextlib
from typing import TYPE_CHECKING, Any, cast

from ..errors import not_found
from ..pubsub import PubSubClosedError, PubSubHandler, match_topic

if TYPE_CHECKING:
    from redis.asyncio.client import PubSub as RedisAsyncPubSub
    from redis.asyncio.client import Redis as RedisAsyncClient


_REDIS_GLOB_ESCAPE = str.maketrans({"*": r"\*", "?": r"\?", "[": r"\[", "\\": r"\\"})


def _escape_redis_glob(segment: str) -> str:
    return segment.translate(_REDIS_GLOB_ESCAPE)


def _pond_pattern_to_redis(pattern: str) -> str:
    if pattern.endswith(".*"):
        return _escape_redis_glob(pattern[:-2]) + "*"
    return _escape_redis_glob(pattern)


class RedisPubSub:
    __slots__ = (
        "_client",
        "_closed",
        "_lock",
        "_owns_client",
        "_pubsub",
        "_reader_task",
        "_subs",
    )

    def __init__(
        self,
        client: RedisAsyncClient,
        *,
        owns_client: bool = False,
    ) -> None:
        self._client = client
        self._pubsub: RedisAsyncPubSub = client.pubsub()
        self._closed = False
        self._owns_client = owns_client
        self._subs: dict[str, PubSubHandler] = {}
        self._lock = asyncio.Lock()
        self._reader_task: asyncio.Task[None] | None = None

    async def subscribe(self, pattern: str, handler: PubSubHandler) -> None:
        async with self._lock:
            if self._closed:
                raise PubSubClosedError()
            if pattern in self._subs:
                raise not_found("pubsub", "pattern already subscribed")
            redis_pattern = _pond_pattern_to_redis(pattern)
            await self._pubsub.psubscribe(redis_pattern)
            self._subs[pattern] = handler
            if self._reader_task is None or self._reader_task.done():
                self._reader_task = asyncio.create_task(self._reader_loop())

    async def unsubscribe(self, pattern: str) -> None:
        async with self._lock:
            if self._closed:
                raise PubSubClosedError()
            if pattern not in self._subs:
                raise not_found("pubsub", "pattern not found")
            redis_pattern = _pond_pattern_to_redis(pattern)
            await self._pubsub.punsubscribe(redis_pattern)
            del self._subs[pattern]

    async def publish(self, topic: str, data: bytes) -> None:
        if self._closed:
            raise PubSubClosedError()
        await self._client.publish(topic, data)

    async def close(self) -> None:
        async with self._lock:
            if self._closed:
                return
            self._closed = True
        if self._reader_task is not None and not self._reader_task.done():
            self._reader_task.cancel()
            with contextlib.suppress(asyncio.CancelledError, Exception):
                await self._reader_task
        with contextlib.suppress(Exception):
            await cast(Any, self._pubsub).aclose()
        if self._owns_client:
            with contextlib.suppress(Exception):
                await cast(Any, self._client).aclose()

    async def _reader_loop(self) -> None:
        while True:
            if self._closed:
                return
            try:
                msg = await self._pubsub.get_message(
                    ignore_subscribe_messages=True,
                    timeout=0.05,
                )
            except asyncio.CancelledError:
                return
            except Exception:
                await asyncio.sleep(0.05)
                continue
            if msg is None:
                continue
            if msg.get("type") != "pmessage":
                continue
            topic = _decode_topic(msg.get("channel"))
            data = msg.get("data")
            if topic is None or not isinstance(data, bytes):
                continue
            await self._dispatch_to_handlers(topic, data)

    async def _dispatch_to_handlers(self, topic: str, data: bytes) -> None:
        async with self._lock:
            handlers = [h for p, h in self._subs.items() if match_topic(p, topic)]
        for handler in handlers:
            try:
                await handler(topic, data)
            except Exception:
                continue


def _decode_topic(value: Any) -> str | None:
    if isinstance(value, bytes):
        try:
            return value.decode("utf-8")
        except UnicodeDecodeError:
            return None
    if isinstance(value, str):
        return value
    return None
