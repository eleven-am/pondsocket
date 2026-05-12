from __future__ import annotations

import asyncio
from dataclasses import dataclass

from .errors import not_found
from .pubsub import PubSubClosedError, PubSubHandler, match_topic


@dataclass(slots=True)
class _Subscription:
    pattern: str
    handler: PubSubHandler
    queue: asyncio.Queue[tuple[str, bytes]]
    task: asyncio.Task[None]


class LocalPubSub:
    __slots__ = ("_buffer_size", "_closed", "_lock", "_subs")

    def __init__(self, *, buffer_size: int = 100) -> None:
        if buffer_size <= 0:
            buffer_size = 100
        self._buffer_size = buffer_size
        self._closed = False
        self._lock = asyncio.Lock()
        self._subs: dict[str, list[_Subscription]] = {}

    async def subscribe(self, pattern: str, handler: PubSubHandler) -> None:
        async with self._lock:
            if self._closed:
                raise PubSubClosedError()
            queue: asyncio.Queue[tuple[str, bytes]] = asyncio.Queue(self._buffer_size)
            task = asyncio.create_task(self._run_subscription(queue, handler))
            sub = _Subscription(pattern=pattern, handler=handler, queue=queue, task=task)
            self._subs.setdefault(pattern, []).append(sub)

    async def _run_subscription(
        self,
        queue: asyncio.Queue[tuple[str, bytes]],
        handler: PubSubHandler,
    ) -> None:
        while True:
            try:
                topic, data = await queue.get()
            except asyncio.CancelledError:
                return
            try:
                await handler(topic, data)
            except Exception:
                continue

    async def unsubscribe(self, pattern: str) -> None:
        async with self._lock:
            if self._closed:
                raise PubSubClosedError()
            if pattern not in self._subs:
                raise not_found("pubsub", "pattern not found")
            subs = self._subs.pop(pattern)
        for sub in subs:
            sub.task.cancel()

    async def publish(self, topic: str, data: bytes) -> None:
        if self._closed:
            raise PubSubClosedError()
        async with self._lock:
            targets: list[_Subscription] = []
            for pattern, subs in self._subs.items():
                if match_topic(pattern, topic):
                    targets.extend(subs)
        for sub in targets:
            try:
                sub.queue.put_nowait((topic, data))
            except asyncio.QueueFull:
                continue

    async def close(self) -> None:
        async with self._lock:
            if self._closed:
                return
            self._closed = True
            all_subs = [s for subs in self._subs.values() for s in subs]
            self._subs.clear()
        for sub in all_subs:
            sub.task.cancel()
