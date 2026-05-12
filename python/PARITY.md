# Go ↔ Python parity

Maps every Go server file to its Python counterpart, every public Go symbol to the Python equivalent, and documents intentional divergences.

The Go reference is `../go/pondsocket/` (server) and `../go/pondsocket-client/` (client, not yet ported).

## File mapping (server core)

| Go file | Python file | Notes |
|---|---|---|
| `go/pondsocket/server.go` | `pondsocket/src/pondsocket/server.py` | `Server` → `PondSocket`. Python version drops HTTP-server lifecycle (BYOS). |
| `go/pondsocket/manager.go` | merged into `server.py` | Path matching lives on `PondSocket.match_endpoint`. No separate Manager class in Python. |
| `go/pondsocket/endpoint.go` | `pondsocket/src/pondsocket/endpoint.py` | 1:1 |
| `go/pondsocket/lobby.go` | `pondsocket/src/pondsocket/lobby.py` | 1:1 |
| `go/pondsocket/channel.go` | `pondsocket/src/pondsocket/channel.py` | 1:1 (largest file in both implementations) |
| `go/pondsocket/connectionContext.go` | `pondsocket/src/pondsocket/contexts/connection_context.py` | I/O removed — pure decision object (BYOS). The adapter does the upgrade. |
| `go/pondsocket/joinContext.go` | `pondsocket/src/pondsocket/contexts/join_context.py` | 1:1 |
| `go/pondsocket/eventContext.go` | `pondsocket/src/pondsocket/contexts/event_context.py` | 1:1 |
| `go/pondsocket/leaveContext.go` | `pondsocket/src/pondsocket/contexts/leave_context.py` | 1:1 |
| `go/pondsocket/outgoingContext.go` | `pondsocket/src/pondsocket/contexts/outgoing_context.py` | 1:1 |
| `go/pondsocket/middleware.go` | `pondsocket/src/pondsocket/middleware.py` | 1:1 (generic `Middleware[Request, Response]`) |
| `go/pondsocket/parser.go` | `pondsocket/src/pondsocket/parser.py` | URL/channel pattern matcher (`:param`, `prefix:param`, `*`, query). 1:1 |
| `go/pondsocket/presence.go` | `pondsocket/src/pondsocket/presence.py` | Uses a `PresenceHost` Protocol to break the Channel↔Presence circular dep. |
| `go/pondsocket/pubsub.go` | `pondsocket/src/pondsocket/pubsub.py` | 1:1 — `PubSub` Protocol + topic helpers |
| `go/pondsocket/pubsub_local.go` | `pondsocket/src/pondsocket/pubsub_local.py` | 1:1 in-memory backend |
| `go/pondsocket/distributed/` | `pondsocket/src/pondsocket/distributed/` | Subpackage with `RedisPubSub` |
| `go/pondsocket/conn.go` | (not in core) `pondsocket-asgi/src/pondsocket_asgi/transport.py` | WebSocket transport lives in the adapter, not the core (stronger BYOS than Go). |
| `go/pondsocket/sseTransport.go` | `pondsocket/src/pondsocket/sse_transport.py` + `pondsocket-asgi/src/pondsocket_asgi/_sse.py` | Transport in core, ASGI HTTP bridge in adapter. |
| `go/pondsocket/transport.go` | `pondsocket/src/pondsocket/transport.py` | `Transport` is a runtime-checkable `Protocol` |
| `go/pondsocket/store.go` | `pondsocket/src/pondsocket/store.py` | `Store[T]` — asyncio-locked map; `List()` renamed to `snapshot()` to avoid mypy confusion with `list[T]` |
| `go/pondsocket/errors.go` + `types.go` Error struct | `pondsocket-common/src/pondsocket_common/errors.py` + `pondsocket/src/pondsocket/errors.py` | Status constants and constructors in comnon; `error_event` (wire conversion) in pondsocket |
| `go/pondsocket/types.go` | `pondsocket/src/pondsocket/types.py` | Server-only types: `Options`, `Event`, `Route`, `User`, `MessageEvent`, enums (`SystemEvents`, `SystemEntity`, `TransportType`, `LeaveReason`, `InternalActions`) |
| `go/pondsocket/utils.go`, `arrays.go` | (no direct Python equivalent) | Python uses built-in lists/dicts; helpers absorbed where needed |
| `go/pondsocket/hooks.go` | `pondsocket/src/pondsocket/hooks.py` | `Hooks` dataclass, `RateLimiter`/`MetricsCollector` Protocols, `TokenBucketRateLimiter`, `with_rate_limiter`, `with_metrics`, `NoopMetrics` |

## File mapping (shared types)

| JS reference (`javascript/comnon/src/`) | Python (`pondsocket-common/src/pondsocket_common/`) |
|---|---|
| `enums.ts` | `enums.py` |
| `schema.ts` | `schemas.py` (pydantic v2 instead of hand-rolled, but same wire shape) |
| `misc/types.ts` | `types.py` |
| `misc/uuid.ts` | `ids.py` (function `uuid()` exposed) |
| `subjects/subject.ts` | `subjects.py::Subject[T]` |
| `subjects/behaviorSubject.ts` | `subjects.py::BehaviorSubject[T]` |
| (no JS analogue — ASGI-specific) | `headers.py::Headers` |
| (Go `errors.go` analogue) | `errors.py::PondError` + status constants + helpers |

## Public symbol mapping

### Construction

| Go | Python |
|---|---|
| `NewServer(options)` | `PondSocket(options=..., pubsub=...)` |
| `server.CreateEndpoint(path, handler, middlewares...)` | `pond.create_endpoint(path, handler, *middlewares)` |
| `endpoint.CreateChannel(path, handler, middlewares...)` | `endpoint.create_channel(pattern, handler, *middlewares)` (returns `Lobby`) |
| `lobby.OnMessage(event, handler, middlewares...)` | `lobby.on_message(event, handler, *middlewares)` |
| `lobby.OnLeave(handler, middlewares...)` | `lobby.on_leave(handler, *middlewares)` |
| `lobby.OnOutgoing(event, handler, middlewares...)` | `lobby.on_outgoing(event, handler, *middlewares)` |

### Contexts (all use `ctx.method(...)` chaining in Go; Python uses `await ctx.method(...)`)

| Go | Python |
|---|---|
| `ctx.Accept()` / `ctx.Decline(code, msg)` | `ctx.accept()` / `await ctx.decline(code, msg)` (on join: `await ctx.accept()`, sync `ctx.accept(**assigns)` for connection) |
| `ctx.Reply(event, payload)` | `await ctx.reply(event, payload)` (on connection: sync `ctx.reply(name, payload)`) |
| `ctx.Broadcast(event, payload)` | `await ctx.broadcast(event, payload)` |
| `ctx.BroadcastTo(event, payload, userIds...)` | `await ctx.broadcast_to(event, payload, *user_ids)` |
| `ctx.BroadcastFrom(event, payload)` | `await ctx.broadcast_from(event, payload)` |
| `ctx.Track(presence)` / `Update` / `UnTrack` | `await ctx.track(...)` / `update_presence(...)` / `untrack(...)` |
| `ctx.Evict(reason, userIds...)` | `await ctx.evict(reason, *user_ids)` |
| `ctx.SetAssigns(key, value)` / `ctx.Assigns(map)` | `await ctx.set_assign(key, value)` / `await ctx.assign(dict)` |
| `ctx.GetUser()` / `ctx.GetAllPresence()` | `ctx.get_user()` / `await ctx.get_all_presence()` |
| `outgoingCtx.Block()` / `Transform(payload)` | `ctx.block()` / `ctx.transform(payload)` (sync) |

### Channel-level methods

| Go | Python |
|---|---|
| `channel.Broadcast(event, payload)` | `await channel.broadcast(event, payload)` |
| `channel.BroadcastTo(event, payload, userIds...)` | `await channel.broadcast_to(event, payload, *user_ids)` |
| `channel.BroadcastFrom(event, payload, exclude)` | `await channel.broadcast_from(event, payload, exclude_user_id)` |
| `channel.Track(userID, presence)` etc. | `await channel.track_presence(user_id, data)` etc. |
| `channel.EvictUser(userID, reason)` | `await channel.evict_user(user_id, reason)` |
| `channel.GetPresence()` | `await channel.get_presence()` |
| `channel.GetAssigns()` | `await channel.get_assigns()` |

### PubSub

| Go | Python |
|---|---|
| `NewLocalPubSub(ctx, bufferSize)` | `LocalPubSub(buffer_size=100)` |
| `distributed.NewRedisPubSub(ctx, client)` | `pondsocket.distributed.RedisPubSub(client, owns_client=False)` |
| `formatTopic(endpoint, channel, event)` | `format_topic(endpoint, channel, event)` |
| `matchTopic(pattern, topic)` | `match_topic(pattern, topic)` |

### Hooks

| Go | Python |
|---|---|
| `Hooks{...}` struct | `Hooks(...)` dataclass |
| `RateLimiter` interface | `RateLimiter` Protocol |
| `MetricsCollector` interface | `MetricsCollector` Protocol |
| `NoopMetrics()` | `NoopMetrics()` |
| `WithRateLimiter(hooks, keyFunc)` | `with_rate_limiter(hooks, key_fn)` |
| `WithMetrics(hooks)` | `with_metrics(hooks)` |
| (no Go equivalent) | `TokenBucketRateLimiter(rate, capacity)` |

### Errors

| Go | Python |
|---|---|
| `Error` struct | `PondError(message, code, channel_name, temporary, details, cause)` |
| `badRequest(...)`, `notFound(...)`, etc. | `bad_request(...)`, `not_found(...)`, etc. |
| `MultiError` | `MultiError(list[BaseException])` |
| `combine(errs...)`, `addError(base, new)` | `combine(*errs)`, `add_error(base, new)` |
| `errorEvent(err)` | `error_event(err)` |
| HTTP status constants `StatusBadRequest`, etc. | `STATUS_BAD_REQUEST`, etc. |

## Intentional divergences

1. **No standalone HTTP server.** Go's `Server.Listen()` owns the HTTP lifecycle. Python's `PondSocket` is pure logic; the adapter (e.g., `PondASGIApp` running under uvicorn) owns I/O. This is the BYOS-strict choice.
2. **WebSocket transport in the adapter, not the core.** Go's `Conn` lives next to `Channel`; Python's `ASGIWebSocketTransport` lives in `pondsocket-asgi`. The core depends on the `Transport` Protocol only — making it possible to write an aiohttp/Tornado adapter without touching the core.
3. **`ConnectionContext` is I/O-free.** Go's `Accept()` performs the WebSocket upgrade. Python's `accept()` only sets the decision; the adapter inspects `is_accepted`/`assigns`/`pending_reply` and does the upgrade.
4. **Presence wire format follows the JS comnon schema.** Payload is `{"presence": [...], "changed": {...}}`. Go's `presencePayload` includes additional internal fields (`userId`, `nodeId`, etc.); Python matches the documented JS spec.
5. **System messages bypass the dispatch queue.** ACK / UNAUTHORIZED / EVICTED / replies go directly to a single transport (via `Channel._send_direct`). Prevents the eviction race where `remove_user` would otherwise tear down the connection before the queued EVICTED frame is delivered. BROADCAST and PRESENCE still go through the dispatch queue (where outgoing middleware can transform/block).
6. **Outgoing middleware is "decide, then send."** Middleware runs to completion; Channel checks `ctx.is_blocked()` afterwards and sends the (possibly transformed) event. Go's design relies on `next()` to chain to the final delivery; Python's makes delivery a separate step so first-matching `on_outgoing` handlers don't accidentally suppress sends.
7. **`async` everywhere.** Every operation that crosses an internal boundary (`Channel.broadcast`, `Store.create`, etc.) is async. Go uses goroutines transparently; Python is explicit.
8. **No `Server.Listen` / `Server.Stop`.** Use your ASGI server's lifecycle. `PondASGIApp` handles `lifespan.shutdown` → calls `pond.close()`.

## Not yet implemented (vs Go)

These are present in Go but deferred in the Python v1:

1. **State sync coordinator for distributed channels.** Go's `syncCoordinator` aggregates STATE_RESPONSE messages over a 500ms window so late-joining nodes see existing presence. Python publishes new events cross-node but doesn't snapshot existing state on first subscription. Workaround: deploy nodes simultaneously, or accept that node-N users see node-A users only after the next presence event.
2. **Cross-node user commands.** Go uses `USER_COMMAND` action events to evict / remove / get users on remote nodes. Python's `evict_user` / `remove_user` are local-only.
3. **Cross-node assigns sync.** Go's `ASSIGNS` action propagates assigns changes. Python keeps assigns local.
4. **Heartbeat / stale-node detection.** Go has a heartbeat mechanism with a 90s expiry window. Python doesn't yet evict phantom users from a dead node.
5. **Subprotocol negotiation in WebSocket.accept.** The adapter accepts without picking a subprotocol from `Sec-WebSocket-Protocol`. Add when needed.
6. **SSE heartbeat / keepalive comments.** No periodic `: ping\n\n` to keep long-lived SSE connections alive through proxies. `pondsocket_asgi._sse.sse_comment(text)` exists for users who want to do it manually.
7. **Server.Listen with graceful shutdown handler.** Use the ASGI server's signal handling.

## Test count

|  | Tests |
|---|---|
| `pondsocket-common` | 85 |
| `pondsocket` core | 100+ |
| `pondsocket-asgi` (incl. SSE) | 100+ |
| **Total** | **302 passing** |

`uv run pytest` runs everything; `uv run mypy` is strict-mode clean across 41 source files.
