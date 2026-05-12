from __future__ import annotations

from pondsocket import ConnectionContext, PondSocket
from pondsocket_asgi import PondASGIApp
from pondsocket_asgi.testing import ASGITestDriver


async def test_lifespan_startup_complete() -> None:
    pond = PondSocket()
    app = PondASGIApp(pond)
    driver = ASGITestDriver(app)
    lifespan = await driver.open_lifespan()
    startup = await lifespan.startup()
    assert startup == {"type": "lifespan.startup.complete"}
    shutdown = await lifespan.shutdown()
    assert shutdown == {"type": "lifespan.shutdown.complete"}


async def test_lifespan_shutdown_closes_pond_endpoints() -> None:
    pond = PondSocket()

    async def auth(ctx: ConnectionContext) -> None:
        ctx.accept()

    endpoint = pond.create_endpoint("/ws", auth)
    assert pond.endpoints == [endpoint]

    app = PondASGIApp(pond)
    driver = ASGITestDriver(app)
    lifespan = await driver.open_lifespan()
    await lifespan.startup()
    await lifespan.shutdown()
    assert pond.endpoints == []


async def test_http_scope_returns_404_for_non_sse_request() -> None:
    pond = PondSocket()
    app = PondASGIApp(pond)
    driver = ASGITestDriver(app)
    status, body = await driver.call_http("/anything")
    assert status == 404
    assert b"not found" in body
