# PondSocket Distributed Protocol v1 — Conformance Requirements

**JavaScript is the definitive reference implementation.** All other ports (Go,
Python, Rust) MUST match JS's on-the-wire behavior exactly, so nodes of different
languages interoperate over the same Redis backend. Where JS's actual behavior
differs from `DISTRIBUTED_PROTOCOL.md`, **JS wins**. And **every port must have
tests** covering the full protocol.

JS reference files:
- `javascript/core/src/abstracts/distributor.ts` (transport: envelope, topics,
  heartbeat publish + liveness, self-filtering).
- `javascript/core/src/engines/channelEngine.ts` (producers/consumers, state-sync,
  node/user tracking, stale-node cleanup).
- `javascript/core/src/types.ts` (`DistributedMessageType` enum + message shapes).

## Envelope (every message)

camelCase, exactly: `protocol` (`"pondsocket.distributed"`), `version` (int `1`),
`type`, `messageId` (any unique string), `sourceNodeId`, `endpointName`,
`channelName`, `timestamp` (epoch ms int). Receivers MUST drop any message whose
`sourceNodeId` equals the local node id (JS filters at the transport layer).

## Namespace

Configurable, default `"default"`, in every runtime. Topics:
- Channel: `<prefix>:v1:<namespace>:<endpoint>:<channel>` — `<endpoint>` has its
  leading `/` stripped, `<channel>` kept verbatim. `<prefix>` default `pondsocket`.
- Heartbeat: `<prefix>:v1:<namespace>:__heartbeat__`.

## Message types — match JS exactly, produce AND consume

Field names camelCase; `?` = optional. Shapes are taken from the JS reference.

1. `USER_MESSAGE` — `fromUserId`, `event`, `payload`, `requestId?`,
   `recipientDescriptor`. `recipientDescriptor` ∈ { string `"ALL_USERS"`, string
   `"ALL_EXCEPT_SENDER"`, JSON array of user-id strings }. Every port MUST emit all
   three forms.
2. `PRESENCE_UPDATE` — `userId`, `presence`. **There is no `event` field**; JOIN vs
   UPDATE is not distinguished on the wire (both upsert). Consumers upsert presence.
3. `PRESENCE_REMOVED` — `userId` only (no `event` field). Consumers remove presence.
4. `ASSIGNS_UPDATE` — producer emits `userId` + full `assigns` snapshot. Consumer
   also accepts a `key`/`value` delta if `assigns` is absent.
5. `EVICT_USER` — `userId`, `reason?`.
6. `USER_REMOVE` — `userId`.
7. `USER_GET_REQUEST` — `userId`, `requestId`, `fromNode`.
8. `USER_GET_RESPONSE` — `userId`, `requestId`, plus the user's `assigns` and
   `presence`. Real+symmetric+tested in all four.
9. `USER_JOINED` — `userId`, `presence`, `assigns`. Feeds node→user tracking for liveness cleanup.
10. `USER_LEFT` — `userId`.
11. `STATE_REQUEST` — `fromNode`. On receive, reply with a
    `STATE_RESPONSE` of local users.
12. `STATE_RESPONSE` — `users`: array of `{ id, assigns, presence }` (element key is
    `id`, NOT `userId`). Go/Python/Rust missing — port from JS.
13. `NODE_HEARTBEAT` — on the heartbeat topic; full envelope + `nodeId`. Publish
    every 30s; a node is dead after 90s without a heartbeat, and on death its
    tracked users/presence/assigns are dropped.

## State-sync + liveness flow (match JS)

- On subscribing a channel, a node emits `STATE_REQUEST`; peers reply
  `STATE_RESPONSE` with their local users.
- Node→user membership is tracked from `STATE_RESPONSE` + `USER_JOINED`/`USER_LEFT`.
- Heartbeats keep nodes alive; a node silent for 90s has its users/presence evicted.
- State synchronization uses `STATE_REQUEST`/`STATE_RESPONSE`; it must not be
  tunneled through presence event values.

## Testing mandate (all ports)

Every port MUST have:
1. Encode/decode round-trip tests for ALL 13 message types asserting the exact
   camelCase wire keys/values above.
2. A `recipientDescriptor` test covering all three forms (esp. ALL_EXCEPT_SENDER).
3. A two-node integration test: presence propagation, ASSIGNS propagation,
   STATE_REQUEST→STATE_RESPONSE sync, and heartbeat-driven stale-node cleanup.
Leave every runtime's build and test suite green.
