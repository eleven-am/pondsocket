from __future__ import annotations

import asyncio
from typing import Generic, TypeVar

from .errors import conflict, not_found

T = TypeVar("T")


class Store(Generic[T]):
    __slots__ = ("_data", "_lock")

    def __init__(self) -> None:
        self._lock = asyncio.Lock()
        self._data: dict[str, T] = {}

    async def create(self, key: str, value: T) -> None:
        async with self._lock:
            if key in self._data:
                raise conflict(key, "Key already exists")
            self._data[key] = value

    async def read(self, key: str) -> T:
        async with self._lock:
            if key not in self._data:
                raise not_found(key, "Key does not exist")
            return self._data[key]

    async def get(self, key: str, default: T | None = None) -> T | None:
        async with self._lock:
            return self._data.get(key, default)

    async def has(self, key: str) -> bool:
        async with self._lock:
            return key in self._data

    async def update(self, key: str, value: T) -> None:
        async with self._lock:
            if key not in self._data:
                raise not_found(key, "Key does not exist")
            self._data[key] = value

    async def upsert(self, key: str, value: T) -> None:
        async with self._lock:
            self._data[key] = value

    async def delete(self, key: str) -> None:
        async with self._lock:
            if key not in self._data:
                raise not_found(key, "Key does not exist")
            del self._data[key]

    async def discard(self, key: str) -> bool:
        async with self._lock:
            if key not in self._data:
                return False
            del self._data[key]
            return True

    async def snapshot(self) -> dict[str, T]:
        async with self._lock:
            return dict(self._data)

    async def keys(self) -> list[str]:
        async with self._lock:
            return list(self._data.keys())

    async def values(self) -> list[T]:
        async with self._lock:
            return list(self._data.values())

    async def get_many(self, *keys: str) -> list[T]:
        async with self._lock:
            return [self._data[k] for k in keys if k in self._data]

    async def len(self) -> int:
        async with self._lock:
            return len(self._data)

    async def clear(self) -> None:
        async with self._lock:
            self._data.clear()
