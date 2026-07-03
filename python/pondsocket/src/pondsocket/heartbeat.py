from __future__ import annotations

import asyncio
import contextlib
import time
from typing import Protocol, runtime_checkable

from .distributed_wire import heartbeat_bytes, parse_envelope
from .pubsub import PubSub, format_heartbeat_topic


@runtime_checkable
class HeartbeatChannel(Protocol):
    node_id: str

    async def evict_node_users(self, node_id: str) -> None: ...


class HeartbeatCoordinator:
    __slots__ = (
        "_channels",
        "_cleanup_task",
        "_closed",
        "_interval",
        "_last_seen",
        "_lock",
        "_node_id",
        "_prefix",
        "_publish_task",
        "_pubsub",
        "_started",
        "_timeout",
        "_topic",
    )

    def __init__(
        self,
        pubsub: PubSub,
        node_id: str,
        *,
        namespace: str = "default",
        key_prefix: str = "pondsocket",
        interval: float = 30.0,
        timeout: float = 90.0,
    ) -> None:
        self._pubsub = pubsub
        self._node_id = node_id
        self._interval = interval
        self._timeout = timeout
        self._prefix = key_prefix
        self._topic = format_heartbeat_topic(namespace=namespace, prefix=key_prefix)
        self._last_seen: dict[str, float] = {}
        self._channels: set[HeartbeatChannel] = set()
        self._lock = asyncio.Lock()
        self._started = False
        self._closed = False
        self._publish_task: asyncio.Task[None] | None = None
        self._cleanup_task: asyncio.Task[None] | None = None

    async def start(self) -> None:
        async with self._lock:
            if self._started or self._closed:
                return
            self._started = True
        with contextlib.suppress(Exception):
            await self._pubsub.subscribe(self._topic, self._on_message)
        self._publish_task = asyncio.create_task(self._publish_loop())
        self._cleanup_task = asyncio.create_task(self._cleanup_loop())

    def register(self, channel: HeartbeatChannel) -> None:
        self._channels.add(channel)

    def unregister(self, channel: HeartbeatChannel) -> None:
        self._channels.discard(channel)

    def note_node(self, node_id: str) -> None:
        if node_id and node_id != self._node_id and node_id not in self._last_seen:
            self._last_seen[node_id] = time.monotonic()

    async def _on_message(self, _topic: str, data: bytes) -> None:
        raw = parse_envelope(data)
        if raw is None:
            return
        if raw.get("type") != "NODE_HEARTBEAT":
            return
        node_id = raw.get("sourceNodeId")
        if not isinstance(node_id, str) or node_id == self._node_id:
            return
        self._last_seen[node_id] = time.monotonic()

    async def _publish_loop(self) -> None:
        try:
            while not self._closed:
                await self._publish_once()
                await asyncio.sleep(self._interval)
        except asyncio.CancelledError:
            return

    async def _publish_once(self) -> None:
        with contextlib.suppress(Exception):
            await self._pubsub.publish(self._topic, heartbeat_bytes(self._node_id))

    async def _cleanup_loop(self) -> None:
        try:
            while not self._closed:
                await asyncio.sleep(self._timeout)
                await self._cleanup_stale_nodes()
        except asyncio.CancelledError:
            return

    async def _cleanup_stale_nodes(self) -> None:
        now = time.monotonic()
        stale = [
            node_id
            for node_id, last_seen in self._last_seen.items()
            if now - last_seen >= self._timeout
        ]
        for node_id in stale:
            for channel in list(self._channels):
                with contextlib.suppress(Exception):
                    await channel.evict_node_users(node_id)
            self._last_seen.pop(node_id, None)

    async def close(self) -> None:
        async with self._lock:
            if self._closed:
                return
            self._closed = True
        for task in (self._publish_task, self._cleanup_task):
            if task is not None and not task.done():
                task.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await task
        with contextlib.suppress(Exception):
            await self._pubsub.unsubscribe(self._topic)
        self._channels.clear()
        self._last_seen.clear()
