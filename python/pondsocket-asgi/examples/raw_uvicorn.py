from __future__ import annotations

from pondsocket import (
    ConnectionContext,
    EventContext,
    JoinContext,
    PondSocket,
)
from pondsocket_asgi import PondASGIApp


async def auth(ctx: ConnectionContext) -> None:
    token = ctx.query.get("token")
    if token != "secret":
        ctx.decline(401, "Invalid token")
        return
    ctx.accept(token=token)


async def on_join(ctx: JoinContext) -> None:
    await ctx.accept()
    await ctx.track({"status": "online"})


async def on_message(ctx: EventContext) -> None:
    await ctx.broadcast(ctx.event_name, ctx.get_payload())


pond = PondSocket()
endpoint = pond.create_endpoint("/socket", auth)
lobby = endpoint.create_channel("/chat/:room_id", on_join)
lobby.on_message("message", on_message)

app = PondASGIApp(pond)


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="127.0.0.1", port=8000)
