# PondSocket Distributed Protocol v1

This file is the canonical contract for server-to-server distribution across the
JavaScript, Go, Python, and Rust implementations of PondSocket. It is separate
from the client WebSocket/SSE wire format.

All runtimes MUST produce and consume the shapes below. A difference between an
implementation and this document is a conformance bug; protocol changes require
updating this document and the cross-runtime tests together.

## Current interoperability status

This document is the target contract, not evidence that every runtime pairing is
currently production-proven. The repository has runtime-local conformance tests,
but it does not yet run a mixed-process Redis integration matrix.

There is also one known topic-level conformance bug: the TypeScript Redis backend
percent-encodes the endpoint and channel topic segments, while Go, Python, Rust,
and the topic contract below keep those segments raw. Consequently, a mixed
TypeScript/non-TypeScript cluster will generally subscribe and publish to
different Redis topics for names containing `/`, `:`, `%`, or other escaped
characters. Homogeneous clusters are supported. Go, Python, and Rust currently
use the same raw topic layout and v1 envelopes, but should still be covered by a
real cross-runtime integration suite before that matrix is described as
production-proven.

The JavaScript reference implementation lives in:

- `javascript/core/src/abstracts/distributor.ts` for envelopes, Redis topics,
  heartbeats, validation, deduplication, and self-filtering.
- `javascript/core/src/engines/channelEngine.ts` for state synchronization,
  node/user tracking, presence, assigns, and stale-node cleanup.
- `javascript/core/src/types.ts` for message types and TypeScript shapes.

## Redis topics

Channel topic:

```text
pondsocket:v1:<namespace>:<endpointName>:<channelName>
```

Heartbeat topic:

```text
pondsocket:v1:<namespace>:__heartbeat__
```

The default namespace is `default`. `endpointName` has its leading slash removed;
`channelName` is kept verbatim. JavaScript and Python can configure the key prefix,
but `pondsocket` MUST be used when interoperating with Go or Rust nodes.

## Envelope

Every message is a UTF-8 JSON object with camelCase fields:

```json
{
  "protocol": "pondsocket.distributed",
  "version": 1,
  "type": "USER_MESSAGE",
  "messageId": "uuid",
  "sourceNodeId": "node-id",
  "endpointName": "api/socket",
  "channelName": "/chat/1",
  "timestamp": 1730000000000
}
```

The envelope fields are required: `protocol`, `version`, `type`, `messageId`,
`sourceNodeId`, `endpointName`, `channelName`, and `timestamp` (epoch milliseconds).
Receivers MUST reject malformed or unsupported envelopes, ignore messages from
their own `sourceNodeId`, and deduplicate repeated `messageId` values within their
configured deduplication window.

## Message types and payloads

`?` marks an optional field. Each runtime MUST both produce and consume all 13
message types.

1. `USER_MESSAGE` — `fromUserId`, `event`, `payload`, `requestId?`, and
   `recipientDescriptor`.
2. `PRESENCE_UPDATE` — `userId` and `presence`. There is no `event` field; join
   and update both upsert presence.
3. `PRESENCE_REMOVED` — `userId`. Consumers remove that user's presence.
4. `ASSIGNS_UPDATE` — producers send `userId` and the complete post-update
   `assigns` snapshot. Consumers also accept `key`/`value` delta fields when
   `assigns` is absent for legacy compatibility.
5. `EVICT_USER` — `userId` and `reason?`.
6. `USER_REMOVE` — `userId`.
7. `USER_GET_REQUEST` — `userId`, `requestId`, and `fromNode`.
8. `USER_GET_RESPONSE` — `userId`, `requestId`, `assigns`, and `presence`.
9. `USER_JOINED` — `userId`, `presence`, and `assigns`.
10. `USER_LEFT` — `userId`.
11. `STATE_REQUEST` — `fromNode`. A receiving peer replies with its local users
    in a `STATE_RESPONSE`.
12. `STATE_RESPONSE` — `users`, an array of `{ id, assigns, presence }`. The user
    identifier key is `id`, not `userId`.
13. `NODE_HEARTBEAT` — sent on the heartbeat topic with the full envelope and
    `nodeId`; `nodeId` MUST equal `sourceNodeId`.

## Recipient descriptors

`USER_MESSAGE.recipientDescriptor` is exactly one of:

```json
"ALL_USERS"
```

```json
"ALL_EXCEPT_SENDER"
```

```json
["user-1", "user-2"]
```

Every runtime MUST support all three forms.

## State synchronization and liveness

- When a node subscribes to a channel, it emits `STATE_REQUEST`; peers answer with
  `STATE_RESPONSE` containing their local users.
- Node-to-user membership is tracked from `STATE_RESPONSE`, `USER_JOINED`, and
  `USER_LEFT` messages.
- A node publishes `NODE_HEARTBEAT` every 30 seconds by default.
- A node is stale after 90 seconds without a heartbeat by default. Its tracked
  users, presence, and assigns are then removed.
- State synchronization MUST use `STATE_REQUEST` and `STATE_RESPONSE`; it must not
  be tunneled through presence event values.

## Conformance tests

Every runtime MUST keep tests for:

1. Encode/decode round trips for all 13 message types, including exact camelCase
   wire keys and values.
2. All three `recipientDescriptor` forms.
3. Two-node integration covering presence propagation, assigns propagation,
   `STATE_REQUEST`/`STATE_RESPONSE`, and heartbeat-driven stale-node cleanup.

All four runtime test suites and a mixed-runtime Redis integration matrix must
remain green for a protocol change to be considered fully conformant. That
mixed-runtime matrix is not yet present in this repository.
