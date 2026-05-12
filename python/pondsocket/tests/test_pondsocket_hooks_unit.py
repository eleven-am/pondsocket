from __future__ import annotations

import asyncio

import pytest

from pondsocket import (
    Channel,
    ChannelOptions,
    Hooks,
    MessageEvent,
    MetricsCollector,
    NoopMetrics,
    RateLimiter,
    TokenBucketRateLimiter,
    User,
    with_metrics,
    with_rate_limiter,
)
from pondsocket.errors import PondError
from pondsocket.types import Event


def _msg(user_id: str = "u", event_name: str = "chat") -> MessageEvent:
    return MessageEvent(
        event=Event(
            action="BROADCAST",
            channel_name="room/1",
            request_id="r",
            event=event_name,
        ),
        user=User(id=user_id),
    )


def test_noop_metrics_satisfies_protocol() -> None:
    assert isinstance(NoopMetrics(), MetricsCollector)


def test_noop_metrics_methods_are_callable() -> None:
    m = NoopMetrics()
    m.connection_opened("u", "/ws")
    m.connection_closed("u", 1.5)
    m.connection_error("u", RuntimeError("x"))
    m.message_received("u", "c", "e", 0)
    m.message_sent("u", "c", "e", 0)
    m.message_broadcast("c", "e", 3)
    m.channel_joined("u", "c")
    m.channel_left("u", "c")
    m.channel_created("c")
    m.channel_destroyed("c")
    m.handler_duration("h", 0.01)
    m.queue_depth("q", 5)
    m.error("comp", RuntimeError("x"))


async def test_token_bucket_satisfies_protocol() -> None:
    rl: RateLimiter = TokenBucketRateLimiter(rate=1.0, capacity=1.0)
    assert isinstance(rl, RateLimiter)


def test_token_bucket_rejects_invalid_config() -> None:
    with pytest.raises(ValueError):
        TokenBucketRateLimiter(rate=0, capacity=1)
    with pytest.raises(ValueError):
        TokenBucketRateLimiter(rate=1, capacity=0)


async def test_token_bucket_allows_up_to_capacity_then_denies() -> None:
    rl = TokenBucketRateLimiter(rate=1.0, capacity=3.0)
    assert await rl.allow("u1") is True
    assert await rl.allow("u1") is True
    assert await rl.allow("u1") is True
    assert await rl.allow("u1") is False


async def test_token_bucket_keys_are_independent() -> None:
    rl = TokenBucketRateLimiter(rate=1.0, capacity=1.0)
    assert await rl.allow("a") is True
    assert await rl.allow("a") is False
    assert await rl.allow("b") is True


async def test_token_bucket_refills_over_time() -> None:
    rl = TokenBucketRateLimiter(rate=100.0, capacity=1.0)
    assert await rl.allow("u") is True
    assert await rl.allow("u") is False
    await asyncio.sleep(0.05)
    assert await rl.allow("u") is True


async def test_token_bucket_reset_clears_state() -> None:
    rl = TokenBucketRateLimiter(rate=1.0, capacity=1.0)
    assert await rl.allow("u") is True
    assert await rl.allow("u") is False
    await rl.reset("u")
    assert await rl.allow("u") is True


async def test_with_rate_limiter_passes_when_no_limiter_configured() -> None:
    hooks = Hooks()
    mw = with_rate_limiter(hooks, key_fn=lambda m: m.user.id)
    called = False

    async def nxt() -> None:
        nonlocal called
        called = True

    channel = await _make_channel()
    await mw(_msg(), channel, nxt)
    assert called is True
    await channel.close()


async def test_with_rate_limiter_raises_when_denied() -> None:
    hooks = Hooks(rate_limiter=TokenBucketRateLimiter(rate=1.0, capacity=1.0))
    mw = with_rate_limiter(hooks, key_fn=lambda m: m.user.id)

    async def nxt() -> None:
        return

    channel = await _make_channel()
    await mw(_msg(), channel, nxt)
    with pytest.raises(PondError) as exc:
        await mw(_msg(), channel, nxt)
    assert exc.value.code == 429
    await channel.close()


async def test_with_rate_limiter_reports_to_metrics_on_deny() -> None:
    recorded: list[tuple[str, BaseException]] = []

    class CapturingMetrics(NoopMetrics):
        def error(self, component: str, err: BaseException) -> None:
            recorded.append((component, err))

    hooks = Hooks(
        rate_limiter=TokenBucketRateLimiter(rate=1.0, capacity=1.0),
        metrics=CapturingMetrics(),
    )
    mw = with_rate_limiter(hooks, key_fn=lambda m: m.user.id)

    async def nxt() -> None:
        return

    channel = await _make_channel()
    await mw(_msg(), channel, nxt)
    with pytest.raises(PondError):
        await mw(_msg(), channel, nxt)
    assert len(recorded) == 1
    assert recorded[0][0] == "rate_limiter"
    await channel.close()


async def test_with_metrics_records_handler_duration_and_message_received() -> None:
    recorded_durations: list[tuple[str, float]] = []
    recorded_received: list[tuple[str, str, str, int]] = []

    class CapturingMetrics(NoopMetrics):
        def handler_duration(self, handler: str, duration_seconds: float) -> None:
            recorded_durations.append((handler, duration_seconds))

        def message_received(
            self, conn_id: str, channel: str, event: str, size: int
        ) -> None:
            recorded_received.append((conn_id, channel, event, size))

    metrics = CapturingMetrics()
    hooks = Hooks(metrics=metrics)
    mw = with_metrics(hooks)

    async def slow_next() -> None:
        await asyncio.sleep(0.01)

    channel = await _make_channel()
    await mw(_msg(user_id="alice", event_name="chat"), channel, slow_next)

    assert recorded_received == [("alice", "room/1", "chat", 0)]
    assert len(recorded_durations) == 1
    assert recorded_durations[0][0] == "chat"
    assert recorded_durations[0][1] >= 0.005
    await channel.close()


async def test_with_metrics_reports_handler_error() -> None:
    recorded: list[tuple[str, BaseException]] = []

    class CapturingMetrics(NoopMetrics):
        def error(self, component: str, err: BaseException) -> None:
            recorded.append((component, err))

    hooks = Hooks(metrics=CapturingMetrics())
    mw = with_metrics(hooks)

    async def failing_next() -> None:
        raise RuntimeError("boom")

    channel = await _make_channel()
    with pytest.raises(RuntimeError):
        await mw(_msg(), channel, failing_next)
    assert len(recorded) == 1
    assert recorded[0][0] == "message_handler"
    await channel.close()


async def test_with_metrics_is_noop_when_no_metrics() -> None:
    hooks = Hooks()
    mw = with_metrics(hooks)
    called = False

    async def nxt() -> None:
        nonlocal called
        called = True

    channel = await _make_channel()
    await mw(_msg(), channel, nxt)
    assert called is True
    await channel.close()


async def _make_channel() -> Channel:
    from pondsocket.types import Options
    ch = Channel(ChannelOptions(name="room/1", options=Options(node_id="n")))
    await ch.start()
    return ch


def test_token_bucket_capacity_property() -> None:
    rl = TokenBucketRateLimiter(rate=5.0, capacity=10.0)
    assert rl.rate == 5.0
    assert rl.capacity == 10.0


async def test_reset_all_clears_every_key() -> None:
    rl = TokenBucketRateLimiter(rate=1.0, capacity=1.0)
    assert await rl.allow("a") is True
    assert await rl.allow("b") is True
    assert await rl.allow("a") is False
    assert await rl.allow("b") is False
    await rl.reset_all()
    assert await rl.allow("a") is True
    assert await rl.allow("b") is True


async def test_token_bucket_concurrent_allow_is_thread_safe() -> None:
    rl = TokenBucketRateLimiter(rate=1.0, capacity=5.0)

    async def attempt() -> bool:
        return await rl.allow("u")

    results = await asyncio.gather(*(attempt() for _ in range(20)))
    allowed = sum(1 for r in results if r)
    assert allowed == 5


async def test_token_bucket_refill_caps_at_capacity() -> None:
    rl = TokenBucketRateLimiter(rate=1000.0, capacity=2.0)
    await rl.allow("u")
    await rl.allow("u")
    assert await rl.allow("u") is False
    await asyncio.sleep(0.5)
    assert await rl.allow("u") is True
    assert await rl.allow("u") is True
    assert await rl.allow("u") is False


