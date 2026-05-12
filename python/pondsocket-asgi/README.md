# pondsocket-asgi

Canonical ASGI adapter for [`pondsocket`](../pondsocket/). Mount under any ASGI host — FastAPI, Starlette, Litestar, Django, raw uvicorn.

Handles WebSocket and SSE (Server-Sent Events) transports. The same `PondSocket` instance serves both — the adapter detects the request type and dispatches.

## Install

```
pip install pondsocket-asgi
```

Pulls in `pondsocket` and `pondsocket-common` automatically.

## Quick start: raw uvicorn

```python
from pondsocket import ConnectionContext, EventContext, JoinContext, PondSocket
from pondsocket_asgi import PondASGIApp


async def auth(ctx: ConnectionContext) -> None:
    ctx.accept()


async def on_join(ctx: JoinContext) -> None:
    await ctx.accept()


async def on_message(ctx: EventContext) -> None:
    await ctx.broadcast(ctx.event_name, ctx.get_payload())


pond = PondSocket()
endpoint = pond.create_endpoint("/socket", auth)
lobby = endpoint.create_channel("/chat/:room_id", on_join)
lobby.on_message("message", on_message)

app = PondASGIApp(pond)
```

```
uvicorn module:app --host 127.0.0.1 --port 8000
```

WebSocket clients connect to `ws://127.0.0.1:8000/socket`.

## Mount under FastAPI

```python
from fastapi import FastAPI
from pondsocket_asgi import PondASGIApp

app = FastAPI()
app.mount("/ws", PondASGIApp(pond))
```

When mounted at `/ws`, the adapter sees `scope["path"]` already stripped of the prefix — register your pond endpoints with the *post-mount* path (e.g. `/socket` resolves at `ws://host/ws/socket`).

See [`examples/`](./examples/) for FastAPI, Starlette, and raw uvicorn variants.

## SSE transport

The same endpoint serves Server-Sent Events to clients that don't have WebSocket:

```
GET /socket
  Accept: text/event-stream
→ 200, response carries X-Connection-ID header and a `CONNECTION` event with `payload.connectionId`, stream of "event: <name>\ndata: <json>\n\n" frames

POST /socket
  X-Connection-ID: <id>
  Content-Type: application/json
  body: <client message JSON>
→ 204

DELETE /socket
  X-Connection-ID: <id>
→ 204
```

Use SSE when WebSocket is blocked (some corporate proxies). The connection handler is the same — the adapter routes by `Accept` header and method.

## Lifespan

`PondASGIApp` handles the ASGI lifespan protocol. On `lifespan.startup`, it acknowledges immediately. On `lifespan.shutdown`, it closes the PondSocket (which closes every endpoint, every channel, every transport).

## Testing

`pondsocket_asgi.testing` exposes an in-memory ASGI driver — no real network needed:

```python
from pondsocket_asgi.testing import ASGITestDriver

driver = ASGITestDriver(PondASGIApp(pond))

# WebSocket
session = await driver.connect_websocket("/socket")
await session.send_client_message(
    action="JOIN_CHANNEL",
    channel_name="/chat/1",
    event="join",
)
ack = await session.receive_json()
await session.disconnect()

# SSE
sse = await driver.connect_sse("/socket")
status, _ = await driver.sse_push(sse.connection_id, {"action": "BROADCAST", ...})
frame = await sse.receive_event()
await sse.disconnect()

# Lifespan
lifespan = await driver.open_lifespan()
await lifespan.startup()
await lifespan.shutdown()
```

## Public API

`from pondsocket_asgi import ...`

- `PondASGIApp` — the adapter itself
- `ASGIWebSocketTransport` — WebSocket transport (you usually don't construct this directly; the app does)

`from pondsocket_asgi.testing import ...`

- `ASGITestDriver`, `WebSocketSession`, `LifespanSession`, `SSESession`

## License

GPL-3.0-or-later.
