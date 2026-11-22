from __future__ import annotations

import asyncio

import pytest

from pondsocket.channel import Channel, ChannelOptions
from pondsocket.contexts.join_context import JoinContext
from pondsocket.errors import PondError
from pondsocket.in_memory_transport import InMemoryTransport
from pondsocket.pubsub_local import LocalPubSub
from pondsocket.types import Event, Options
from pondsocket_common import ClientActions, PresenceEventTypes, ServerActions


class _ToggleFailPubSub(LocalPubSub):
    def __init__(self) -> None:
        super().__init__()
        self.fail_publish = False

    async def publish(self, topic: str, data: bytes) -> None:
        if self.fail_publish:
            raise RuntimeError("pubsub unavailable")
        await super().publish(topic, data)


def _join_event() -> Event:
    return Event(
        action=ClientActions.JOIN_CHANNEL.value,
        channel_name="room/distributed",
        request_id="join-1",
        event="join",
        payload={},
    )


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


async def test_close_releases_all_channel_owned_user_state() -> None:
    ch = await _channel()
    await _seat(ch, "alice")
    await ch.track_presence("alice", {"status": "online"})

    await ch.close()

    assert await ch.user_count() == 0
    assert await ch.get_assigns() == {}
    assert await ch.get_presence() == {}


async def test_dispatch_queue_timeout_is_reported_instead_of_dropping() -> None:
    class StalledTransport(InMemoryTransport):
        def __init__(self, id: str) -> None:
            super().__init__(id)
            self.send_started = asyncio.Event()

        async def send_event(self, event: Event) -> None:
            del event
            self.send_started.set()
            await asyncio.Event().wait()

    ch = Channel(
        ChannelOptions(
            name="room/backpressure",
            options=Options(
                node_id="node-a",
                internal_queue_buffer=1,
                internal_queue_timeout=0.01,
                send_timeout=1.0,
            ),
        )
    )
    await ch.start()
    transport = StalledTransport("slow")
    await ch.add_user(transport)

    await ch.broadcast("one", {})
    await transport.send_started.wait()
    await ch.broadcast("two", {})
    with pytest.raises(TimeoutError):
        await ch.broadcast("three", {})
    await ch.close()


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


async def test_add_user_rolls_back_when_distributed_publish_fails() -> None:
    bus = _ToggleFailPubSub()
    ch = Channel(
        ChannelOptions(
            name="room/distributed",
            options=Options(node_id="node-a"),
            endpoint_path="/api/socket",
            pubsub=bus,
        )
    )
    await ch.start()
    bus.fail_publish = True

    with pytest.raises(RuntimeError, match="pubsub unavailable"):
        await ch.add_user(InMemoryTransport("alice", assigns={"role": "admin"}))

    assert not await ch.has_user("alice")
    assert "alice" not in await ch.get_assigns()
    await ch.close()
    await bus.close()


async def test_join_context_remains_unanswered_when_registration_fails() -> None:
    bus = _ToggleFailPubSub()
    ch = Channel(
        ChannelOptions(
            name="room/distributed",
            options=Options(node_id="node-a"),
            endpoint_path="/api/socket",
            pubsub=bus,
        )
    )
    await ch.start()
    bus.fail_publish = True
    transport = InMemoryTransport("alice")
    ctx = JoinContext(channel=ch, event=_join_event(), transport=transport)

    with pytest.raises(RuntimeError, match="pubsub unavailable"):
        await ctx.accept()

    assert not ctx.has_responded
    assert not ctx.is_accepted
    assert not await ch.has_user("alice")
    await ch.close()
    await bus.close()


async def test_join_ack_failure_rolls_back_membership() -> None:
    class FailingTransport(InMemoryTransport):
        async def send_event(self, event: Event) -> None:
            del event
            raise RuntimeError("send failed")

    ch = await _channel("room/distributed")
    transport = FailingTransport("alice")
    ctx = JoinContext(channel=ch, event=_join_event(), transport=transport)

    with pytest.raises(RuntimeError, match="send failed"):
        await ctx.accept()

    assert not ctx.has_responded
    assert not ctx.is_accepted
    assert not await ch.has_user("alice")
    await ch.close()


async def test_remove_user_finishes_cleanup_when_distributed_publish_fails() -> None:
    bus = _ToggleFailPubSub()
    left: list[str] = []

    async def on_leave(ctx):  # type: ignore[no-untyped-def]
        left.append(ctx.user.id)

    ch = Channel(
        ChannelOptions(
            name="room/distributed",
            options=Options(node_id="node-a"),
            endpoint_path="/api/socket",
            pubsub=bus,
            leave_handler=on_leave,
        )
    )
    await ch.start()
    await ch.add_user(InMemoryTransport("alice", assigns={"role": "admin"}))
    await ch.track_presence("alice", {"status": "online"})
    bus.fail_publish = True

    with pytest.raises(RuntimeError, match="pubsub unavailable"):
        await ch.remove_user("alice")

    assert not await ch.has_user("alice")
    assert "alice" not in await ch.get_assigns()
    assert "alice" not in await ch.get_presence()
    assert left == ["alice"]
    assert not ch.is_active
    await bus.close()


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


async def test_stalled_transport_is_closed_after_send_timeout() -> None:
    class StalledTransport(InMemoryTransport):
        def __init__(self, id: str) -> None:
            super().__init__(id)
            self.close_called = asyncio.Event()

        async def send_event(self, event):  # type: ignore[no-untyped-def]
            del event
            await asyncio.Event().wait()

        async def close(self) -> None:
            await super().close()
            self.close_called.set()

    ch = Channel(
        ChannelOptions(
            name="room/timeout",
            options=Options(node_id="node-a", send_timeout=0.01),
        )
    )
    await ch.start()
    transport = StalledTransport("slow")
    await ch.add_user(transport)

    await ch.broadcast("chat", {"text": "hi"})

    await asyncio.wait_for(transport.close_called.wait(), timeout=1.0)
    assert not transport.is_active()
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
