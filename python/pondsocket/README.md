# pondsocket

PondSocket - channel-based real-time framework for Python. Bring-your-own-server design: the core knows nothing about HTTP frameworks or WebSocket libraries. You build the layered API (Server -> Endpoint -> Channel -> User), and an adapter (e.g. [`pondsocket-asgi`](../pondsocket-asgi/)) bridges it to your HTTP stack.

## Install

```
pip install pondsocket
pip install "pondsocket[redis]"   # multi-node deployments
```

## Concepts

```
PondSocket     top-level facade, holds endpoints
  └── Endpoint   one URL path, runs the connection auth handler, owns transports
        └── Lobby     one channel pattern (e.g. "/chat/:room_id"), holds shared middleware
              └── Channel    one live channel instance, holds users + presence
```

### Five contexts, scoped to lifecycle moments

| Context | When | What it decides / does |
|---|---|---|
| `ConnectionContext` | HTTP upgrade | accept / decline socket, set initial assigns |
| `JoinContext` | per channel join | accept / decline membership, track presence |
| `EventContext` | per incoming event | broadcast, reply, update presence/assigns, evict |
| `OutgoingContext` | per recipient, per send | transform payload or block delivery |
| `LeaveContext` | user gone | final cleanup (user already removed) |

## Minimal example

```python
from pondsocket import ConnectionContext, EventContext, JoinContext, PondSocket


async def auth(ctx: ConnectionContext) -> None:
    if ctx.query.get("token") != "secret":
        ctx.decline(401, "Invalid token")
        return
    ctx.accept(user_id=ctx.query.get("uid", "anon"))


async def on_join(ctx: JoinContext) -> None:
    await ctx.accept()
    await ctx.track({"status": "online"})


async def on_message(ctx: EventContext) -> None:
    await ctx.broadcast(ctx.event_name, ctx.get_payload())


pond = PondSocket()
endpoint = pond.create_endpoint("/api/socket", auth)
lobby = endpoint.create_channel("/chat/:room_id", on_join)
lobby.on_message("message", on_message)
```

To actually serve traffic, wrap `pond` in an adapter — see [`pondsocket-asgi`](../pondsocket-asgi/) for the canonical one.

## Distributed (multi-node)

```python
from redis.asyncio import Redis
from pondsocket import PondSocket
from pondsocket.distributed import RedisPubSub

redis = Redis(host="localhost", port=6379)
pubsub = RedisPubSub(redis, owns_client=True)
pond = PondSocket(pubsub=pubsub)
```

Cross-node broadcasts and presence events propagate via Redis PSUBSCRIBE. `node_id` is auto-generated when a pubsub is configured. Each broadcast carries the `nodeId` so emitters filter their own echo.

See [`pondsocket.distributed.RedisPubSub`](src/pondsocket/distributed/redis_pubsub.py) for the implementation. The `PubSub` protocol is implementable for other backends.

## Hooks & metrics

```python
from pondsocket import Hooks, NoopMetrics, Options, PondSocket, TokenBucketRateLimiter

hooks = Hooks(
    rate_limiter=TokenBucketRateLimiter(rate=10, capacity=20),
    metrics=NoopMetrics(),
)
pond = PondSocket(options=Options(hooks=hooks))
```

Auto-fired by the framework: `connection_opened`/`connection_closed`, `channel_created`/`channel_destroyed`/`channel_joined`/`channel_left`, plus `on_connect`/`on_disconnect`/`before_join`/`after_join`/`before_message`/`after_message`.

Opt-in middleware factories for per-handler instrumentation:

```python
from pondsocket import with_metrics, with_rate_limiter

lobby.on_message(
    "message",
    on_message,
    with_rate_limiter(hooks, key_fn=lambda msg: msg.user.id),
    with_metrics(hooks),
)
```

## Testing

`InMemoryTransport` is a public test utility that satisfies the `Transport` protocol without any network I/O:

```python
from pondsocket import InMemoryTransport

t = InMemoryTransport("alice")
await endpoint.register_transport(t)

await t.push_inbound(some_event)
sent = await t.receive_sent()
```

## Public API

`from pondsocket import ...`

- Core: `PondSocket`, `Endpoint`, `Lobby`, `Channel`, `Options`, `Event`, `User`, `MessageEvent`, `RecipientSpec`
- Contexts: `ConnectionContext`, `JoinContext`, `EventContext`, `LeaveContext`, `OutgoingContext`
- Transport: `Transport` (Protocol), `InMemoryTransport`, `SSEServerTransport`, `TransportType`, `OnCloseCallback`, `OnMessageHandler`
- PubSub: `PubSub` (Protocol), `LocalPubSub`, `format_topic`, `match_topic`, `PubSubClosedError`
- Hooks: `Hooks`, `RateLimiter`, `MetricsCollector`, `NoopMetrics`, `TokenBucketRateLimiter`, `with_rate_limiter`, `with_metrics`
- Wire: `event_to_json`, `event_to_wire`, `parse_inbound_text`, `wire_to_event`, `event_to_pubsub_bytes`, `pubsub_bytes_to_event`
- Errors: `PondError`, status constructors, `error_event`

## License

GPL-3.0-or-later.
