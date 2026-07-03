# PondSocket Distributed Protocol v1 — Conformance Requirements

**JavaScript is the definitive reference implementation.** All other ports (Go,
Python, Rust) MUST match JS's on-the-wire behavior exactly, so nodes of different
languages interoperate over the same Redis backend. Where JS's actual behavior
differs from `DISTRIBUTED_PROTOCOL.md`, **JS wins**. And **every port must have
tests** covering the full protocol (Rust currently has ~2 tests total — that is
the biggest gap).

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

Configurable, default `"default"` (JS supports this; Go/Python/Rust hardcode it —
make it configurable). Topics:
- Channel: `<prefix>:v1:<namespace>:<endpoint>:<channel>` — `<endpoint>` has its
  leading `/` stripped, `<channel>` kept verbatim. `<prefix>` default `pondsocket`.
- Heartbeat: `<prefix>:v1:<namespace>:__heartbeat__`.

## Message types — match JS exactly, produce AND consume

Field names camelCase; `?` = optional. Shapes are taken from the JS reference.

1. `USER_MESSAGE` — `fromUserId`, `event`, `payload`, `requestId?`,
   `recipientDescriptor`. `recipientDescriptor` ∈ { string `"ALL_USERS"`, string
   `"ALL_EXCEPT_SENDER"`, JSON array of user-id strings }. Every port MUST emit all
   three forms. **Rust bug:** it collapses ALL_EXCEPT_SENDER to ALL_USERS — fix.
2. `PRESENCE_UPDATE` — `userId`, `presence`. **JS emits NO `event` field**; JOIN vs
   UPDATE is not distinguished on the wire (both upsert). Go/Python/Rust currently
   emit an `event` field — **remove it** to match JS. Consumers upsert presence.
3. `PRESENCE_REMOVED` — `userId` only (no `event` field). Consumers remove presence.
4. `ASSIGNS_UPDATE` — producer emits `userId` + full `assigns` snapshot. Consumer
   also accepts a `key`/`value` delta if `assigns` is absent (JS does both on the
   consume side). Go currently nests key/value in `payload` and omits the clean
   top-level shape — align to JS (`userId` + `assigns` on produce).
5. `EVICT_USER` — `userId`, `reason?`.
6. `USER_REMOVE` — `userId`. (JS declares the type but has no producer/consumer;
   Go/Python/Rust use it. Decision: make it a real, symmetric, tested feature in
   ALL four — implement the JS producer/consumer so the reference is complete.)
7. `USER_GET_REQUEST` — `userId`, `requestId`, `fromNode`. (Same as USER_REMOVE:
   Go has full produce+consume; make it real+symmetric+tested in all four,
   including JS. **Rust bug:** produces but never consumes — add the decoder.)
8. `USER_GET_RESPONSE` — `userId`, `requestId`, plus the user's `assigns` and
   `presence`. Real+symmetric+tested in all four.
9. `USER_JOINED` — `userId`, `presence`, `assigns` (Go/Python/Rust missing — port
   from JS). Feeds node→user tracking for liveness cleanup.
10. `USER_LEFT` — `userId` (Go/Python/Rust missing — port from JS).
11. `STATE_REQUEST` — `fromNode` (Go/Python/Rust missing). On receive, reply with a
    `STATE_RESPONSE` of local users.
12. `STATE_RESPONSE` — `users`: array of `{ id, assigns, presence }` (element key is
    `id`, NOT `userId`). Go/Python/Rust missing — port from JS.
13. `NODE_HEARTBEAT` — on the heartbeat topic; full envelope + `nodeId`. Publish
    every 30s; a node is dead after 90s without a heartbeat, and on death its
    tracked users/presence/assigns are dropped. Go/Python/Rust are missing the
    entire subsystem — port from JS (`distributor.ts` publish + `channelEngine.ts`
    `#nodeLastSeen` / `#cleanupStaleNodes` / node-user tracking).

## State-sync + liveness flow (match JS)

- On subscribing a channel, a node emits `STATE_REQUEST`; peers reply
  `STATE_RESPONSE` with their local users.
- Node→user membership is tracked from `STATE_RESPONSE` + `USER_JOINED`/`USER_LEFT`.
- Heartbeats keep nodes alive; a node silent for 90s has its users/presence evicted.
- **Go must stop tunneling sync through `PRESENCE_UPDATE` `event` values**
  (`presence:sync_request` etc.) and use the real `STATE_REQUEST`/`STATE_RESPONSE`.

## Testing mandate (all ports)

Every port MUST have:
1. Encode/decode round-trip tests for ALL 13 message types asserting the exact
   camelCase wire keys/values above.
2. A `recipientDescriptor` test covering all three forms (esp. ALL_EXCEPT_SENDER).
3. A two-node integration test: presence propagation, ASSIGNS propagation,
   STATE_REQUEST→STATE_RESPONSE sync, and heartbeat-driven stale-node cleanup.
Rust must go from ~2 tests to full coverage of the above. Leave build + test green.
