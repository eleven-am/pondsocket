# PondSocket Rust

Rust implementation of PondSocket with the same wire protocol as the JavaScript,
Go, and Python implementations.

## Crates

| Crate | Purpose |
|---|---|
| `pondsocket-common` | Shared wire enums, schemas, message parsing, UUID helper |
| `pondsocket` | Core server model: endpoints, lobbies, channels, contexts, presence, pubsub |
| `pondsocket-axum` | Axum WebSocket adapter |
| `pondsocket-client` | Typed asynchronous client with bounded queues and automatic reconnect |

## Features

- Endpoint connection authorization.
- Channel pattern matching with `:param`, `prefix:param`, wildcard, and query support.
- Join, leave, broadcast, reply, system events, and presence events.
- Server-side assigns.
- Outgoing message handlers that can transform or block delivery.
- Local in-memory pubsub.
- Optional Redis pubsub with `pondsocket/redis`.
- Cross-node broadcast delivery, targeted broadcast recipient metadata, assigns updates, and user eviction commands.
- Rust client reconnect with exponential backoff and jitter, channel rejoin, join/response timeouts,
  ping/pong liveness detection (any inbound WebSocket frame confirms peer liveness), bounded inbound
  messages, and an observable runtime error stream.
- Axum server Ping/Pong and read-idle supervision, with connection capacity reserved before upgrade.

## Axum upgrade safety

Use `configure_websocket_upgrade` before calling `on_upgrade`. It applies frame/message allocation
limits and reserves a `max_connections` slot before the WebSocket and connection handler are
created. Pass the returned permit to `handle_socket_with_permit`; dropping it on any rejected or
failed upgrade automatically releases the slot.

```rust,ignore
let (upgrade, permit) =
    pondsocket_axum::configure_websocket_upgrade(upgrade, &endpoint)?;
Ok(upgrade.on_upgrade(move |socket| {
    pondsocket_axum::handle_socket_with_permit(pond, socket, request, permit)
}))
```

## Development

```bash
cargo test
cargo test -p pondsocket --features redis
```

### Redis security boundary

Redis credentials and the configured PondSocket namespace form a privileged
trust boundary. Any principal that can publish into that namespace can inject
distributed PondSocket control events. For non-local deployments, use a
`rediss://` URL, a dedicated ACL user with access limited to the required
PondSocket topics, a deployment-specific unguessable namespace, and normal
credential rotation. Do not share the Redis user or namespace with untrusted
applications. PondSocket validates its distributed envelope shape, but it does
not add an application-level signature to messages delivered by Redis. For a
TLS Redis client, PondSocket installs Rustls's Ring provider only when the
application has not already selected a process-wide crypto provider.

## Migrating to 0.2

The reliability work in 0.2 intentionally changes several public APIs and must
not be released as a 0.1.x patch:

- `Options` and `ClientOptions` have new timeout, liveness, queue, and size
  controls. Code that constructs either with a struct literal should add the
  new fields or use `..Default::default()`.
- PubSub handlers are asynchronous. Wrap async callbacks with
  `pubsub_handler`, and use `subscribe_pattern` when wildcard matching is
  intended; `subscribe` now always means an exact topic.
- `PondSocket::create_endpoint` and `Endpoint::create_channel` now return a
  `Result` because creation is rejected once shutdown begins.
- `Channel::add_user` operates on `Arc<Channel>` so membership cleanup can be
  tied safely to the channel lifecycle.
- Axum integrations must reserve capacity with
  `configure_websocket_upgrade` and pass the returned permit to
  `handle_socket_with_permit` as shown above.

## Wire Shape

Inbound client messages use the existing camelCase protocol:

```json
{
  "action": "BROADCAST",
  "event": "message",
  "channelName": "/chat/1",
  "requestId": "request-id",
  "payload": {}
}
```

Presence messages keep the JS/Python payload shape:

```json
{
  "action": "PRESENCE",
  "event": "JOIN",
  "channelName": "/chat/1",
  "requestId": "request-id",
  "payload": {
    "presence": [],
    "changed": {}
  }
}
```

