from __future__ import annotations

import asyncio
import time
from collections.abc import Awaitable, Callable
from dataclasses import dataclass
from typing import TYPE_CHECKING, Protocol, TypeAlias, runtime_checkable

from .errors import too_many_requests
from .types import Event, MessageEvent, User

if TYPE_CHECKING:
    from .channel import Channel
    from .transport import Transport


@runtime_checkable
class RateLimiter(Protocol):
    async def allow(self, key: str) -> bool: ...
    async def reset(self, key: str) -> None: ...


@runtime_checkable
class MetricsCollector(Protocol):
    def connection_opened(self, conn_id: str, endpoint: str) -> None: ...
    def connection_closed(self, conn_id: str, duration_seconds: float) -> None: ...
    def connection_error(self, conn_id: str, err: BaseException) -> None: ...
    def message_received(
        self, conn_id: str, channel: str, event: str, size: int
    ) -> None: ...
    def message_sent(
        self, conn_id: str, channel: str, event: str, size: int
    ) -> None: ...
    def message_broadcast(
        self, channel: str, event: str, recipient_count: int
    ) -> None: ...
    def channel_joined(self, user_id: str, channel: str) -> None: ...
    def channel_left(self, user_id: str, channel: str) -> None: ...
    def channel_created(self, channel: str) -> None: ...
    def channel_destroyed(self, channel: str) -> None: ...
    def handler_duration(self, handler: str, duration_seconds: float) -> None: ...
    def queue_depth(self, queue: str, depth: int) -> None: ...
    def error(self, component: str, err: BaseException) -> None: ...


class NoopMetrics:
    __slots__ = ()

    def connection_opened(self, conn_id: str, endpoint: str) -> None:
        return

    def connection_closed(self, conn_id: str, duration_seconds: float) -> None:
        return

    def connection_error(self, conn_id: str, err: BaseException) -> None:
        return

    def message_received(
        self, conn_id: str, channel: str, event: str, size: int
    ) -> None:
        return

    def message_sent(
        self, conn_id: str, channel: str, event: str, size: int
    ) -> None:
        return

    def message_broadcast(
        self, channel: str, event: str, recipient_count: int
    ) -> None:
        return

    def channel_joined(self, user_id: str, channel: str) -> None:
        return

    def channel_left(self, user_id: str, channel: str) -> None:
        return

    def channel_created(self, channel: str) -> None:
        return

    def channel_destroyed(self, channel: str) -> None:
        return

    def handler_duration(self, handler: str, duration_seconds: float) -> None:
        return

    def queue_depth(self, queue: str, depth: int) -> None:
        return

    def error(self, component: str, err: BaseException) -> None:
        return


OnConnect: TypeAlias = Callable[["Transport"], Awaitable[None]]
OnDisconnect: TypeAlias = Callable[["Transport"], Awaitable[None]]
BeforeJoin: TypeAlias = Callable[[User, str], Awaitable[None]]
AfterJoin: TypeAlias = Callable[[User, str], Awaitable[None]]
BeforeMessage: TypeAlias = Callable[[Event], Awaitable[None]]
AfterMessage: TypeAlias = Callable[[Event, BaseException | None], Awaitable[None]]


@dataclass(slots=True)
class Hooks:
    rate_limiter: RateLimiter | None = None
    channel_rate_limiter: RateLimiter | None = None
    metrics: MetricsCollector | None = None
    on_connect: OnConnect | None = None
    on_disconnect: OnDisconnect | None = None
    before_join: BeforeJoin | None = None
    after_join: AfterJoin | None = None
    before_message: BeforeMessage | None = None
    after_message: AfterMessage | None = None


@dataclass(slots=True)
class _Bucket:
    tokens: float
    last_refill: float


class TokenBucketRateLimiter:
    __slots__ = ("_buckets", "_capacity", "_lock", "_rate")

    def __init__(self, *, rate: float, capacity: float) -> None:
        if rate <= 0:
            raise ValueError("rate must be > 0")
        if capacity <= 0:
            raise ValueError("capacity must be > 0")
        self._rate = rate
        self._capacity = capacity
        self._buckets: dict[str, _Bucket] = {}
        self._lock = asyncio.Lock()

    @property
    def rate(self) -> float:
        return self._rate

    @property
    def capacity(self) -> float:
        return self._capacity

    async def allow(self, key: str) -> bool:
        async with self._lock:
            now = time.monotonic()
            bucket = self._buckets.get(key)
            if bucket is None:
                bucket = _Bucket(tokens=self._capacity, last_refill=now)
                self._buckets[key] = bucket
            elapsed = now - bucket.last_refill
            bucket.tokens = min(self._capacity, bucket.tokens + elapsed * self._rate)
            bucket.last_refill = now
            if bucket.tokens >= 1.0:
                bucket.tokens -= 1.0
                return True
            return False

    async def reset(self, key: str) -> None:
        async with self._lock:
            self._buckets.pop(key, None)

    async def reset_all(self) -> None:
        async with self._lock:
            self._buckets.clear()


def with_rate_limiter(
    hooks: Hooks,
    key_fn: Callable[[MessageEvent], str],
) -> Callable[
    [MessageEvent, Channel, Callable[[], Awaitable[None]]],
    Awaitable[None],
]:
    async def middleware(
        message: MessageEvent,
        _channel: Channel,
        nxt: Callable[[], Awaitable[None]],
    ) -> None:
        if hooks.rate_limiter is None:
            await nxt()
            return
        key = key_fn(message)
        allowed = await hooks.rate_limiter.allow(key)
        if not allowed:
            err = too_many_requests(
                message.event.channel_name, "Rate limit exceeded"
            )
            if hooks.metrics is not None:
                hooks.metrics.error("rate_limiter", err)
            raise err
        await nxt()

    return middleware


def with_metrics(
    hooks: Hooks,
) -> Callable[
    [MessageEvent, Channel, Callable[[], Awaitable[None]]],
    Awaitable[None],
]:
    async def middleware(
        message: MessageEvent,
        _channel: Channel,
        nxt: Callable[[], Awaitable[None]],
    ) -> None:
        if hooks.metrics is None:
            await nxt()
            return
        metrics = hooks.metrics
        start = time.monotonic()
        try:
            await nxt()
        except Exception as e:
            metrics.error("message_handler", e)
            raise
        finally:
            duration = time.monotonic() - start
            metrics.message_received(
                message.user.id,
                message.event.channel_name,
                message.event.event,
                0,
            )
            metrics.handler_duration(message.event.event, duration)

    return middleware
