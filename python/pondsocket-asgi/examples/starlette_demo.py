from __future__ import annotations

from starlette.applications import Starlette
from starlette.responses import JSONResponse
from starlette.routing import Mount, Route

from pondsocket import (
    ConnectionContext,
    EventContext,
    JoinContext,
    PondSocket,
)
from pondsocket_asgi import PondASGIApp


async def auth(ctx: ConnectionContext) -> None:
    ctx.accept()


async def on_join(ctx: JoinContext) -> None:
    await ctx.accept()


async def on_message(ctx: EventContext) -> None:
    await ctx.broadcast(ctx.event_name, ctx.get_payload())


pond = PondSocket()
endpoint = pond.create_endpoint("/", auth)
lobby = endpoint.create_channel("/chat/:room_id", on_join)
lobby.on_message("message", on_message)


async def healthcheck(_request: object) -> JSONResponse:
    return JSONResponse({"status": "ok"})


app = Starlette(
    routes=[
        Route("/", endpoint=healthcheck),
        Mount("/ws", app=PondASGIApp(pond)),
    ]
)


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=8000)
