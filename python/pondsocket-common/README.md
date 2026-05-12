# pondsocket-common

Shared wire types, schemas, and reactive primitives consumed by both `pondsocket` (server) and (eventually) `pondsocket-client` (Python client). Mirrors the role of `@eleven-am/pondsocket-common` in the JavaScript monorepo.

This package has **one runtime dependency**: pydantic v2.

## Install

```
pip install pondsocket-common
```

In a workspace it's installed automatically via `uv sync`.

## What's in it

| Module | Purpose |
|---|---|
| `pondsocket_common.enums` | `ServerActions`, `ClientActions`, `ChannelState`, `PresenceEventTypes`, `ErrorTypes`, `ChannelReceiver`, `SystemSender`, `Events`, `PubSubEvents` — all `StrEnum` with exact wire strings |
| `pondsocket_common.schemas` | Pydantic v2 models: `ClientMessage`, `ServerMessage`, `PresenceMessage`, `ChannelEvent`, plus `parse_client_message` / `parse_server_message` / `parse_presence_message` / `parse_channel_event` with comnon-style `ValidationError` |
| `pondsocket_common.types` | `UserData`, `IncomingConnection`, `PresencePayload`, `PubSubEvent` variants, type aliases (`PondMessage`, `PondPresence`, `PondAssigns`, `JoinParams`) |
| `pondsocket_common.headers` | `Headers` — case-insensitive view over ASGI-shape `list[tuple[bytes, bytes]]` |
| `pondsocket_common.subjects` | `Subject[T]` and `BehaviorSubject[T]` — synchronous reactive primitives mirroring the JS comnon impl |
| `pondsocket_common.errors` | `PondError`, status constants (400/401/403/404/409/429/500/503/504), constructor helpers (`bad_request`, `unauthorized`, …), `MultiError` |
| `pondsocket_common.ids` | `uuid()` — dashed v4 string |

## Wire format

Outbound client → server (`ClientMessage`):

```json
{
  "action": "BROADCAST",
  "event": "message",
  "channelName": "/chat/42",
  "requestId": "<uuid>",
  "payload": {"text": "hi"}
}
```

Server → client (`ServerMessage` | `PresenceMessage`) discriminated on `action`:

```json
{ "action": "SYSTEM",  "event": "ACKNOWLEDGE", "channelName": "/chat/42", "requestId": "...", "payload": {} }
{ "action": "PRESENCE", "event": "JOIN",       "channelName": "/chat/42", "requestId": "...", "payload": {"presence": [...], "changed": {...}} }
```

Field names on the wire are camelCase. The Python models use snake_case attributes (`request_id`, `channel_name`) with pydantic aliases. Use `model_dump(by_alias=True)` to serialize to wire form.

## Parity with `@eleven-am/pondsocket-common`

- Enums have identical string values.
- `Subject`/`BehaviorSubject` mirror the JS classes 1:1 (synchronous fan-out, `size` property, `close()` semantics, `BehaviorSubject` initial-value replay on subscribe).
- `ValidationError(message, path?)` formats as `"path: message"` to match.
- `uuid()` returns a dashed lowercase hex string (matches `crypto.randomUUID()`).

The pydantic models are the canonical schema definitions on the Python side; the JS package uses hand-rolled validators with the same field-name shape.

## License

GPL-3.0-or-later.
