# PondSocket Rust

Rust implementation of PondSocket with the same wire protocol as the JavaScript,
Go, and Python implementations.

## Crates

| Crate | Purpose |
|---|---|
| `pondsocket-common` | Shared wire enums, schemas, message parsing, UUID helper |
| `pondsocket` | Core server model: endpoints, lobbies, channels, contexts, presence, pubsub |
| `pondsocket-axum` | Axum WebSocket adapter |

## Features

- Endpoint connection authorization.
- Channel pattern matching with `:param`, `prefix:param`, wildcard, and query support.
- Join, leave, broadcast, reply, system events, and presence events.
- Server-side assigns.
- Outgoing message handlers that can transform or block delivery.
- Local in-memory pubsub.
- Optional Redis pubsub with `pondsocket/redis`.
- Cross-node broadcast delivery, targeted broadcast recipient metadata, assigns updates, and user eviction commands.

## Development

```bash
cargo test
cargo test -p pondsocket --features redis
```

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

