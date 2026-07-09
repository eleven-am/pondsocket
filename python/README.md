# PondSocket - Python

A channel-based real-time framework for Python. Parallel port of the [Go](../go/) and [TypeScript](../javascript/) implementations, with the same wire protocol and conceptual model: **Server → Endpoint → Channel → User**.

Designed around **bring-your-own-server**: the core never imports a WebSocket library. The canonical ASGI adapter mounts under FastAPI, Starlette, Litestar, raw uvicorn — anywhere ASGI runs.

## Packages

| Package | What it is | PyPI name |
|---|---|---|
| [`pondsocket-common`](./pondsocket-common/) | Shared wire types, schemas (pydantic), reactive primitives | `pondsocket-common` |
| [`pondsocket`](./pondsocket/) | Server core: channels, presence, contexts, middleware, hooks, distributed | `pondsocket` |
| [`pondsocket-asgi`](./pondsocket-asgi/) | Canonical ASGI adapter (WebSocket + SSE) | `pondsocket-asgi` |
| [`pondsocket-client`](./pondsocket-client/) | Python client (WebSocket) | `pondsocket-client` |

Optional extras: `pondsocket[redis]` for multi-node deployments.

## Install (development)

```
cd python
uv sync --all-packages
uv run pytest
```

## 30-second example (FastAPI)

```python
from fastapi import FastAPI
from pondsocket import ConnectionContext, EventContext, JoinContext, PondSocket
from pondsocket_asgi import PondASGIApp


async def auth(ctx: ConnectionContext) -> None:
    if ctx.query.get("token") != "secret":
        ctx.decline(401, "Invalid token")
        return
    ctx.accept()


async def on_join(ctx: JoinContext) -> None:
    await ctx.accept()
    await ctx.track({"status": "online"})


async def on_message(ctx: EventContext) -> None:
    await ctx.broadcast(ctx.event_name, ctx.get_payload())


pond = PondSocket()
endpoint = pond.create_endpoint("/", auth)
lobby = endpoint.create_channel("/chat/:room_id", on_join)
lobby.on_message("message", on_message)

app = FastAPI()
app.mount("/ws", PondASGIApp(pond))
```

Then connect from the JS client at `ws://localhost:8000/ws/?token=secret` and join `/chat/<room>`.

## Layout

```
python/
├── README.md                 (this file)
├── PARITY.md                 Go ↔ Python file and symbol mapping
├── pyproject.toml            uv workspace root, shared tool config
├── Makefile                  sync / lint / type / test / clean
├── pondsocket-common/        shared types & schemas
├── pondsocket/               server core
└── pondsocket-asgi/          ASGI adapter
```

## Status

417 tests passing. ruff clean. mypy strict in 53 source files. The WebSocket wire format is aligned with the existing JS client, and the SSE `CONNECTION` event now carries the same `connectionId` field the browser client expects.

See [`PARITY.md`](./PARITY.md) for the Go → Python mapping and a list of intentional divergences.

## Development

```
make sync         # uv sync --all-packages
make lint         # ruff check
make format       # ruff format
make type         # mypy strict
make test         # pytest
make check        # lint + type + test
make clean        # remove caches and venv
```

## Build

Build each publishable package from its own directory:

```
cd pondsocket-common && uv build
cd ../pondsocket && uv build
cd ../pondsocket-asgi && uv build
cd ../pondsocket-client && uv build
```

Do not publish from the workspace root.

## License

GPL-3.0-or-later (matching the rest of the repo).
