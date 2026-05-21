from __future__ import annotations

import asyncio
from typing import Protocol, runtime_checkable

from pondsocket_common import PondPresence, PresenceEventTypes

from .errors import conflict, not_found


@runtime_checkable
class PresenceHost(Protocol):
    name: str
    node_id: str

    async def publish_presence_change(
        self,
        *,
        event_type: PresenceEventTypes,
        changed_user_id: str,
        changed: PondPresence,
        presence_snapshot: list[PondPresence],
    ) -> None: ...


class PresenceClient:
    __slots__ = ("_host", "_lock", "_store")

    def __init__(self, host: PresenceHost) -> None:
        self._host = host
        self._lock = asyncio.Lock()
        self._store: dict[str, PondPresence] = {}

    async def track(self, user_id: str, data: PondPresence) -> None:
        async with self._lock:
            if user_id in self._store:
                raise conflict(self._host.name, f"Already tracking presence for {user_id}")
            self._store[user_id] = data
            snapshot = list(self._store.values())
        await self._host.publish_presence_change(
            event_type=PresenceEventTypes.JOIN,
            changed_user_id=user_id,
            changed=data,
            presence_snapshot=snapshot,
        )

    async def update(self, user_id: str, data: PondPresence) -> None:
        async with self._lock:
            if user_id not in self._store:
                raise not_found(self._host.name, f"Not tracking presence for {user_id}")
            self._store[user_id] = data
            snapshot = list(self._store.values())
        await self._host.publish_presence_change(
            event_type=PresenceEventTypes.UPDATE,
            changed_user_id=user_id,
            changed=data,
            presence_snapshot=snapshot,
        )

    async def upsert(self, user_id: str, data: PondPresence) -> None:
        async with self._lock:
            existed = user_id in self._store
            self._store[user_id] = data
            snapshot = list(self._store.values())
        await self._host.publish_presence_change(
            event_type=PresenceEventTypes.UPDATE if existed else PresenceEventTypes.JOIN,
            changed_user_id=user_id,
            changed=data,
            presence_snapshot=snapshot,
        )

    async def untrack(self, user_id: str) -> None:
        async with self._lock:
            if user_id not in self._store:
                return
            old = self._store.pop(user_id)
            snapshot = list(self._store.values())
        await self._host.publish_presence_change(
            event_type=PresenceEventTypes.LEAVE,
            changed_user_id=user_id,
            changed=old,
            presence_snapshot=snapshot,
        )

    async def get(self, user_id: str) -> PondPresence:
        async with self._lock:
            if user_id not in self._store:
                raise not_found(self._host.name, f"No presence for {user_id}")
            return dict(self._store[user_id])

    async def get_all(self) -> dict[str, PondPresence]:
        async with self._lock:
            return {k: dict(v) for k, v in self._store.items()}

    async def has(self, user_id: str) -> bool:
        async with self._lock:
            return user_id in self._store

    async def keys(self) -> list[str]:
        async with self._lock:
            return list(self._store.keys())

    async def set_remote(self, user_id: str, data: PondPresence) -> None:
        async with self._lock:
            self._store[user_id] = data

    async def discard(self, user_id: str) -> None:
        async with self._lock:
            self._store.pop(user_id, None)

    async def values(self) -> list[PondPresence]:
        async with self._lock:
            return [dict(v) for v in self._store.values()]

    async def len(self) -> int:
        async with self._lock:
            return len(self._store)
