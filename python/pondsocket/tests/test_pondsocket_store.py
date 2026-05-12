from __future__ import annotations

import pytest

from pondsocket.errors import PondError
from pondsocket.store import Store


async def test_create_and_read_roundtrip() -> None:
    s: Store[int] = Store()
    await s.create("a", 1)
    assert await s.read("a") == 1


async def test_create_conflict_raises() -> None:
    s: Store[int] = Store()
    await s.create("a", 1)
    with pytest.raises(PondError) as exc:
        await s.create("a", 2)
    assert exc.value.code == 409


async def test_read_missing_raises() -> None:
    s: Store[int] = Store()
    with pytest.raises(PondError) as exc:
        await s.read("missing")
    assert exc.value.code == 404


async def test_get_returns_default_for_missing() -> None:
    s: Store[int] = Store()
    assert await s.get("missing") is None
    assert await s.get("missing", 7) == 7


async def test_has_reflects_presence() -> None:
    s: Store[int] = Store()
    assert not await s.has("k")
    await s.create("k", 1)
    assert await s.has("k")


async def test_update_changes_value() -> None:
    s: Store[int] = Store()
    await s.create("k", 1)
    await s.update("k", 2)
    assert await s.read("k") == 2


async def test_update_missing_raises() -> None:
    s: Store[int] = Store()
    with pytest.raises(PondError) as exc:
        await s.update("missing", 1)
    assert exc.value.code == 404


async def test_upsert_creates_or_updates() -> None:
    s: Store[int] = Store()
    await s.upsert("k", 1)
    assert await s.read("k") == 1
    await s.upsert("k", 2)
    assert await s.read("k") == 2


async def test_delete_removes_or_raises() -> None:
    s: Store[int] = Store()
    await s.create("k", 1)
    await s.delete("k")
    assert not await s.has("k")
    with pytest.raises(PondError):
        await s.delete("k")


async def test_discard_is_graceful() -> None:
    s: Store[int] = Store()
    assert not await s.discard("missing")
    await s.create("k", 1)
    assert await s.discard("k")
    assert not await s.has("k")


async def test_snapshot_returns_copy() -> None:
    s: Store[int] = Store()
    await s.create("a", 1)
    await s.create("b", 2)
    snap = await s.snapshot()
    snap["injected"] = 999
    assert not await s.has("injected")


async def test_keys_values_get_many() -> None:
    s: Store[int] = Store()
    await s.create("a", 1)
    await s.create("b", 2)
    await s.create("c", 3)
    assert sorted(await s.keys()) == ["a", "b", "c"]
    assert sorted(await s.values()) == [1, 2, 3]
    got = await s.get_many("a", "c", "missing")
    assert sorted(got) == [1, 3]


async def test_len_and_clear() -> None:
    s: Store[int] = Store()
    await s.create("a", 1)
    await s.create("b", 2)
    assert await s.len() == 2
    await s.clear()
    assert await s.len() == 0
