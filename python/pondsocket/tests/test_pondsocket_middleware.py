from __future__ import annotations

from collections.abc import Awaitable, Callable

import pytest

from pondsocket.middleware import Middleware, execute_with_middleware


class Req:
    def __init__(self, value: int = 0) -> None:
        self.value = value


class Resp:
    def __init__(self) -> None:
        self.trail: list[str] = []


async def test_handle_with_no_middleware_calls_final_directly() -> None:
    m: Middleware[Req, Resp] = Middleware()
    req = Req()
    resp = Resp()

    async def final(r: Req, s: Resp) -> None:
        s.trail.append(f"final:{r.value}")

    await m.handle(req, resp, final)
    assert resp.trail == ["final:0"]


async def test_middleware_runs_in_registration_order() -> None:
    m: Middleware[Req, Resp] = Middleware()

    async def h1(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        resp.trail.append("h1:before")
        await nxt()
        resp.trail.append("h1:after")

    async def h2(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        resp.trail.append("h2:before")
        await nxt()
        resp.trail.append("h2:after")

    await m.use(h1, h2)

    async def final(_r: Req, s: Resp) -> None:
        s.trail.append("final")

    resp = Resp()
    await m.handle(Req(), resp, final)
    assert resp.trail == ["h1:before", "h2:before", "final", "h2:after", "h1:after"]


async def test_middleware_can_short_circuit_by_not_calling_next() -> None:
    m: Middleware[Req, Resp] = Middleware()

    async def gate(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        resp.trail.append("gate")

    await m.use(gate)

    async def final(_r: Req, s: Resp) -> None:
        s.trail.append("final")

    resp = Resp()
    await m.handle(Req(), resp, final)
    assert resp.trail == ["gate"]


async def test_compose_concatenates_handlers_in_order() -> None:
    a: Middleware[Req, Resp] = Middleware()
    b: Middleware[Req, Resp] = Middleware()

    async def h_a(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        resp.trail.append("a")
        await nxt()

    async def h_b(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        resp.trail.append("b")
        await nxt()

    await a.use(h_a)
    await b.use(h_b)
    composed = await a.compose(b)

    async def final(_r: Req, s: Resp) -> None:
        s.trail.append("final")

    resp = Resp()
    await composed.handle(Req(), resp, final)
    assert resp.trail == ["a", "b", "final"]


async def test_compose_does_not_share_handler_list_with_originals() -> None:
    a: Middleware[Req, Resp] = Middleware()
    b: Middleware[Req, Resp] = Middleware()

    async def h(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        await nxt()

    await a.use(h)
    composed = await a.compose(b)

    async def h2(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        await nxt()

    await composed.use(h2)
    assert len(a) == 1
    assert len(composed) == 2


async def test_handler_exceptions_propagate() -> None:
    m: Middleware[Req, Resp] = Middleware()

    async def broken(req: Req, resp: Resp, nxt: Callable[[], Awaitable[None]]) -> None:
        raise ValueError("boom")

    await m.use(broken)

    async def final(_r: Req, _s: Resp) -> None:
        pass

    with pytest.raises(ValueError, match="boom"):
        await m.handle(Req(), Resp(), final)


async def test_execute_with_middleware_per_handler_chain() -> None:
    trail: list[str] = []

    class Ctx:
        pass

    async def m1(_c: Ctx, nxt: Callable[[], Awaitable[None]]) -> None:
        trail.append("m1")
        await nxt()

    async def m2(_c: Ctx, nxt: Callable[[], Awaitable[None]]) -> None:
        trail.append("m2")
        await nxt()

    async def handler(_c: Ctx) -> None:
        trail.append("handler")

    await execute_with_middleware(Ctx(), handler, [m1, m2])
    assert trail == ["m1", "m2", "handler"]


async def test_execute_with_middleware_empty_chain_calls_handler() -> None:
    trail: list[str] = []

    class Ctx:
        pass

    async def handler(_c: Ctx) -> None:
        trail.append("handler")

    await execute_with_middleware(Ctx(), handler, [])
    assert trail == ["handler"]
