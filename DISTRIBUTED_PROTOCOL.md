# PondSocket Distributed Protocol v1

All PondSocket implementations use this protocol for server-to-server distribution.
It is intentionally separate from the client WebSocket/SSE wire format.

## Topics

Channel topic:

```text
pondsocket:v1:<namespace>:<endpointName>:<channelName>
```

Heartbeat topic:

```text
pondsocket:v1:<namespace>:__heartbeat__
```

The default namespace is `default`.

## Envelope

Every distributed message is a UTF-8 JSON object:

```json
{
  "protocol": "pondsocket.distributed",
  "version": 1,
  "type": "USER_MESSAGE",
  "messageId": "uuid",
  "sourceNodeId": "node-id",
  "endpointName": "/api/socket",
  "channelName": "/chat/1",
  "timestamp": 1730000000000
}
```

Implementations must ignore messages with their own `sourceNodeId`.

## Message Types

- `STATE_REQUEST`
- `STATE_RESPONSE`
- `USER_JOINED`
- `USER_LEFT`
- `USER_MESSAGE`
- `PRESENCE_UPDATE`
- `PRESENCE_REMOVED`
- `ASSIGNS_UPDATE`
- `EVICT_USER`
- `USER_REMOVE`
- `USER_GET_REQUEST`
- `USER_GET_RESPONSE`
- `NODE_HEARTBEAT`

## Recipient Descriptors

`USER_MESSAGE.recipientDescriptor` is one of:

```json
"ALL_USERS"
```

```json
"ALL_EXCEPT_SENDER"
```

```json
["user-1", "user-2"]
```

These names match the existing client/common enums.

## Presence

Presence updates are distributed as:

```json
{
  "type": "PRESENCE_UPDATE",
  "userId": "user-1",
  "event": "JOIN",
  "presence": { "status": "online" }
}
```

Presence removals are distributed as:

```json
{
  "type": "PRESENCE_REMOVED",
  "userId": "user-1",
  "event": "LEAVE"
}
```

Implementations may include a client-shaped `payload` for local compatibility,
but `userId` and `presence` are the canonical cross-runtime fields.

## Assigns

Assign updates are distributed as:

```json
{
  "type": "ASSIGNS_UPDATE",
  "userId": "user-1",
  "key": "role",
  "value": "admin",
  "assigns": { "role": "admin" }
}
```

`assigns` is the canonical post-update assigns snapshot for the user. `key` and
`value` are optional delta fields and exist so runtimes can process partial
updates without losing compatibility.
