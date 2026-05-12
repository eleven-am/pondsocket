from __future__ import annotations

from dataclasses import dataclass, field

import pytest

from pondsocket.errors import PondError
from pondsocket.presence import PresenceClient, PresenceHost
from pondsocket_common import PondPresence, PresenceEventTypes


@dataclass
class _PublishedEvent:
    event_type: PresenceEventTypes
    changed_user_id: str
    changed: PondPresence
    snapshot: list[PondPresence]


@dataclass
class _FakeHost:
    name: str = "room/1"
    node_id: str = "node-a"
    published: list[_PublishedEvent] = field(default_factory=list)

    async def publish_presence_change(
        self,
        *,
        event_type: PresenceEventTypes,
        changed_user_id: str,
        changed: PondPresence,
        presence_snapshot: list[PondPresence],
    ) -> None:
        self.published.append(
            _PublishedEvent(
                event_type=event_type,
                changed_user_id=changed_user_id,
                changed=dict(changed),
                snapshot=[dict(p) for p in presence_snapshot],
            )
        )


def _client() -> tuple[PresenceClient, _FakeHost]:
    host = _FakeHost()
    return PresenceClient(host), host


def test_fake_host_satisfies_protocol() -> None:
    host = _FakeHost()
    assert isinstance(host, PresenceHost)


async def test_track_adds_and_publishes_join() -> None:
    pc, host = _client()
    await pc.track("u1", {"status": "online"})
    assert await pc.has("u1")
    assert host.published == [
        _PublishedEvent(
            event_type=PresenceEventTypes.JOIN,
            changed_user_id="u1",
            changed={"status": "online"},
            snapshot=[{"status": "online"}],
        )
    ]


async def test_track_duplicate_raises_conflict() -> None:
    pc, _ = _client()
    await pc.track("u1", {})
    with pytest.raises(PondError) as exc:
        await pc.track("u1", {})
    assert exc.value.code == 409


async def test_update_changes_and_publishes() -> None:
    pc, host = _client()
    await pc.track("u1", {"status": "online"})
    await pc.update("u1", {"status": "away"})
    assert (await pc.get("u1")) == {"status": "away"}
    assert host.published[-1].event_type is PresenceEventTypes.UPDATE
    assert host.published[-1].changed == {"status": "away"}


async def test_update_missing_raises_not_found() -> None:
    pc, _ = _client()
    with pytest.raises(PondError) as exc:
        await pc.update("missing", {})
    assert exc.value.code == 404


async def test_upsert_joins_then_updates() -> None:
    pc, host = _client()
    await pc.upsert("u1", {"status": "online"})
    assert host.published[-1].event_type is PresenceEventTypes.JOIN
    await pc.upsert("u1", {"status": "away"})
    assert host.published[-1].event_type is PresenceEventTypes.UPDATE


async def test_untrack_removes_and_publishes_leave_with_old_data() -> None:
    pc, host = _client()
    await pc.track("u1", {"status": "online"})
    await pc.untrack("u1")
    assert not await pc.has("u1")
    assert host.published[-1].event_type is PresenceEventTypes.LEAVE
    assert host.published[-1].changed == {"status": "online"}
    assert host.published[-1].snapshot == []


async def test_untrack_missing_is_silent() -> None:
    pc, host = _client()
    await pc.untrack("missing")
    assert host.published == []


async def test_snapshot_includes_all_tracked() -> None:
    pc, host = _client()
    await pc.track("u1", {"name": "a"})
    await pc.track("u2", {"name": "b"})
    last = host.published[-1]
    assert sorted(last.snapshot, key=lambda p: p["name"]) == [{"name": "a"}, {"name": "b"}]


async def test_get_returns_defensive_copy() -> None:
    pc, _ = _client()
    await pc.track("u1", {"status": "online"})
    got = await pc.get("u1")
    got["status"] = "MUTATED"
    assert (await pc.get("u1")) == {"status": "online"}


async def test_get_all_returns_defensive_copies() -> None:
    pc, _ = _client()
    await pc.track("u1", {"status": "online"})
    snap = await pc.get_all()
    snap["u1"]["status"] = "MUTATED"
    fresh = await pc.get_all()
    assert fresh["u1"]["status"] == "online"


async def test_get_missing_raises_not_found() -> None:
    pc, _ = _client()
    with pytest.raises(PondError) as exc:
        await pc.get("missing")
    assert exc.value.code == 404


async def test_keys_values_len() -> None:
    pc, _ = _client()
    await pc.track("u1", {"name": "a"})
    await pc.track("u2", {"name": "b"})
    assert sorted(await pc.keys()) == ["u1", "u2"]
    assert await pc.len() == 2
    vals = await pc.values()
    assert sorted(vals, key=lambda p: p["name"]) == [{"name": "a"}, {"name": "b"}]
