from __future__ import annotations

import asyncio

import pytest

from pondsocket.channel import Channel, ChannelOptions
from pondsocket.errors import PondError
from pondsocket.in_memory_transport import InMemoryTransport
from pondsocket.types import Options
from pondsocket_common import PresenceEventTypes, ServerActions


async def _channel(name: str = "room/1") -> Channel:
    ch = Channel(ChannelOptions(name=name, options=Options(node_id="node-a")))
    await ch.start()
    return ch


async def _seat(ch: Channel, user_id: str) -> InMemoryTransport:
    t = InMemoryTransport(user_id, assigns={"role": "user"})
    await ch.add_user(t)
    return t


async def test_start_close_lifecycle() -> None:
    ch = await _channel()
    assert ch.is_active is True
    await ch.close()
    assert ch.is_active is False


async def test_add_user_initializes_assigns_from_transport() -> None:
    ch = await _channel()
    t = await _seat(ch, "u1")
    assert await ch.has_user("u1")
    user = await ch.get_user("u1")
    assert user.assigns == {"role": "user"}
    await ch.close()
    await t.close()


async def test_add_user_duplicate_raises_conflict() -> None:
    ch = await _channel()
    t = InMemoryTransport("u1")
    await ch.add_user(t)
    with pytest.raises(PondError) as exc:
        await ch.add_user(t)
    assert exc.value.code == 409
    await ch.close()


async def test_remove_user_clears_state() -> None:
    ch = await _channel()
    await _seat(ch, "u1")
    await ch.remove_user("u1")
    assert not await ch.has_user("u1")
    with pytest.raises(PondError):
        await ch.get_user("u1")
    await ch.close()


async def test_broadcast_delivers_to_all_users() -> None:
    ch = await _channel()
    a = await _seat(ch, "a")
    b = await _seat(ch, "b")
    await ch.broadcast("chat", {"text": "hi"})

    ev_a = await a.receive_sent()
    ev_b = await b.receive_sent()
    assert ev_a.event == "chat"
    assert ev_a.payload == {"text": "hi"}
    assert ev_a.channel_name == "room/1"
    assert ev_a.action == ServerActions.BROADCAST.value
    assert ev_b.event == "chat"
    await ch.close()


async def test_broadcast_to_specific_users_only() -> None:
    ch = await _channel()
    a = await _seat(ch, "a")
    b = await _seat(ch, "b")
    c = await _seat(ch, "c")
    await ch.broadcast_to("chat", {"text": "hi"}, "a", "c")

    ev_a = await a.receive_sent()
    ev_c = await c.receive_sent()
    assert ev_a.event == "chat"
    assert ev_c.event == "chat"
    await asyncio.sleep(0.02)
    assert b.outbox_size() == 0
    await ch.close()


async def test_broadcast_from_excludes_sender() -> None:
    ch = await _channel()
    a = await _seat(ch, "a")
    b = await _seat(ch, "b")
    await ch.broadcast_from("chat", {"text": "hi"}, "a")

    ev_b = await b.receive_sent()
    assert ev_b.event == "chat"
    await asyncio.sleep(0.02)
    assert a.outbox_size() == 0
    await ch.close()


async def test_send_acknowledge_targets_only_one_user() -> None:
    ch = await _channel()
    a = await _seat(ch, "a")
    b = await _seat(ch, "b")
    await ch.send_acknowledge("req-1", "a")
    ev = await a.receive_sent()
    assert ev.event == "ACKNOWLEDGE"
    assert ev.action == ServerActions.SYSTEM.value
    assert ev.request_id == "req-1"
    await asyncio.sleep(0.02)
    assert b.outbox_size() == 0
    await ch.close()


async def test_track_presence_emits_join_event_to_tracked_users() -> None:
    ch = await _channel()
    a = await _seat(ch, "a")
    b = await _seat(ch, "b")
    await ch.track_presence("a", {"status": "online"})
    await ch.track_presence("b", {"status": "online"})

    a_events = []
    for _ in range(4):
        try:
            a_events.append(await a.receive_sent(wait=0.1))
        except TimeoutError:
            break

    join_events = [e for e in a_events if e.action == ServerActions.PRESENCE.value]
    assert any(e.event == PresenceEventTypes.JOIN.value for e in join_events)
    assert any(
        isinstance(e.payload, dict) and e.payload.get("changed") == {"status": "online"}
        for e in join_events
    )
    await ch.close()
    await b.close()


async def test_update_presence_emits_update_event() -> None:
    ch = await _channel()
    a = await _seat(ch, "a")
    await ch.track_presence("a", {"status": "online"})
    join_ev = await a.receive_sent()
    assert join_ev.event == PresenceEventTypes.JOIN.value

    await ch.update_presence("a", {"status": "away"})
    ev = await a.receive_sent()
    assert ev.event == PresenceEventTypes.UPDATE.value
    assert ev.payload == {
        "presence": [{"status": "away"}],
        "changed": {"status": "away"},
    }
    await ch.close()


async def test_untrack_presence_emits_leave_event() -> None:
    ch = await _channel()
    a = await _seat(ch, "a")
    b = await _seat(ch, "b")
    await ch.track_presence("a", {"status": "online"})
    await ch.track_presence("b", {"status": "online"})

    await _consume_until(b, PresenceEventTypes.JOIN.value, last_changed_user_count=2)
    await _consume_until(a, PresenceEventTypes.JOIN.value, last_changed_user_count=2)

    await ch.untrack_presence("a")

    ev = await b.receive_sent()
    assert ev.event == PresenceEventTypes.LEAVE.value
    assert ev.action == ServerActions.PRESENCE.value
    await ch.close()


async def _consume_until(
    t: InMemoryTransport,
    event_name: str,
    *,
    last_changed_user_count: int,
) -> None:
    for _ in range(20):
        try:
            ev = await t.receive_sent(wait=0.05)
        except TimeoutError:
            await asyncio.sleep(0.01)
            continue
        if (
            ev.event == event_name
            and isinstance(ev.payload, dict)
            and len(ev.payload.get("presence", [])) >= last_changed_user_count
        ):
            return


async def test_get_presence_returns_snapshot() -> None:
    ch = await _channel()
    await _seat(ch, "a")
    await ch.track_presence("a", {"status": "online"})
    snap = await ch.get_presence()
    assert snap == {"a": {"status": "online"}}
    await ch.close()


async def test_update_assign_modifies_user_assigns() -> None:
    ch = await _channel()
    await _seat(ch, "a")
    await ch.update_assign("a", "role", "admin")
    user = await ch.get_user("a")
    assert user.assigns["role"] == "admin"
    await ch.close()


async def test_update_assign_unknown_user_raises_not_found() -> None:
    ch = await _channel()
    with pytest.raises(PondError) as exc:
        await ch.update_assign("ghost", "k", "v")
    assert exc.value.code == 404
    await ch.close()


async def test_remove_user_fires_leave_handler_with_reason() -> None:
    captured: list[tuple[str, str]] = []

    async def leave(ctx):  # type: ignore[no-untyped-def]
        captured.append((ctx.user.id, ctx.reason))

    ch = Channel(
        ChannelOptions(
            name="room/2",
            options=Options(node_id="node-a"),
            leave_handler=leave,
        )
    )
    await ch.start()
    await _seat(ch, "alice")
    await ch.remove_user("alice", reason="explicit_leave")
    assert captured == [("alice", "explicit_leave")]
    await ch.close()


async def test_remove_user_on_empty_fires_on_destroy() -> None:
    fired = asyncio.Event()

    async def on_destroy() -> None:
        fired.set()

    ch = Channel(
        ChannelOptions(
            name="room/3",
            options=Options(node_id="node-a"),
            on_destroy=on_destroy,
        )
    )
    await ch.start()
    await _seat(ch, "alice")
    await ch.remove_user("alice")
    await asyncio.wait_for(fired.wait(), timeout=1.0)
    await ch.close()


async def test_evict_user_sends_system_then_removes() -> None:
    ch = await _channel()
    a = await _seat(ch, "alice")
    await ch.evict_user("alice", "no spam")
    ev = await a.receive_sent()
    assert ev.event == "EVICTED"
    assert ev.payload == {"reason": "no spam"}
    assert not await ch.has_user("alice")
    await ch.close()


async def test_send_after_close_is_silent() -> None:
    ch = await _channel()
    await ch.close()
    await ch.broadcast("chat", {})
