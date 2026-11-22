# PondSocket

PondSocket is a channel-based real-time framework with compatible server and client implementations for TypeScript, Go, Python, and Rust. It provides WebSocket messaging, Server-Sent Events where supported, presence, private per-user assigns, request/reply events, middleware, and Redis-backed multi-node delivery.

The common model is:

```text
server -> endpoint -> channel pattern -> concrete channel -> user
```

An endpoint authenticates a connection. A channel handler authorizes membership. Event handlers then reply, broadcast, update presence or assigns, and remove users without turning the entire connection lifecycle into one callback.

## Implementations

| Runtime | Packages | Documentation |
|---|---|---|
| TypeScript / Node.js | Core server, browser/Node client, Express and NestJS adapters | [TypeScript packages](#typescript-packages) |
| Go | Server and client modules | [Go server](./go/pondsocket/README.md), [Go client](./go/pondsocket-client/README.md) |
| Python | Server core, ASGI adapter, client and shared wire types | [Python workspace](./python/README.md) |
| Rust | Server, Axum adapter, client and shared wire types | [Rust workspace](./rust/README.md) |

## Compatibility

The Go, Rust, Python, and JavaScript WebSocket clients and servers use the same client/server JSON actions and are designed for any-to-any connections. Applications still need matching endpoint names, channel names, event names, authentication, and payload expectations.

The browser JavaScript `SSEClient` can connect to the TypeScript, Go, and Python SSE transports. The Rust/Axum adapter currently provides WebSocket transport, not SSE.

Server-to-server distribution is a separate protocol. All runtimes implement the same v1 JSON envelope, but mixed-runtime Redis clusters are not universally interoperable today: the TypeScript backend percent-encodes Redis topic segments while Go, Python, Rust, and the written contract use raw segments. Homogeneous clusters are supported. Go/Python/Rust have aligned topic and envelope implementations, but the repository does not yet contain a mixed-process integration suite proving that deployment matrix. See the [distributed protocol](./DISTRIBUTED_PROTOCOL.md) for the contract and current limitation.

## TypeScript quick start

Install the server and client:

```bash
npm install @eleven-am/pondsocket @eleven-am/pondsocket-client
```

Create a server:

```ts
import { PondSocket } from '@eleven-am/pondsocket';

const pond = new PondSocket();

const endpoint = pond.createEndpoint('/v1/socket', (ctx) => {
    if (!ctx.query.token) {
        ctx.decline('Missing token', 401);
        return;
    }

    ctx.accept({ role: 'member' });
});

const chat = endpoint.createChannel('/chat/:roomId', (ctx) => {
    ctx.accept({ displayName: ctx.joinParams.displayName });
    ctx.trackPresence({ online: true });
});

chat.onEvent('message', (ctx) => {
    ctx.broadcast('message', ctx.payload);
});

pond.listen(3000);
```

Connect a client:

```ts
import { PondClient } from '@eleven-am/pondsocket-client';

const client = new PondClient('ws://localhost:3000/v1/socket', {
    token: 'connection-token',
});

client.connect();

const channel = client.createChannel('/chat/general', {
    displayName: 'Ada',
});

channel.onMessageEvent('message', console.log);
channel.join();
channel.sendMessage('message', { text: 'hello' });
```

Call `client.disconnect()` and `await pond.closeAsync()` during shutdown so timers, sockets, subscriptions, and broker resources are released.

## TypeScript packages

| Package | Purpose | Documentation |
|---|---|---|
| `@eleven-am/pondsocket` | Node.js server core and Redis distribution | [Core](./javascript/core/README.md) |
| `@eleven-am/pondsocket-client` | Browser/Node WebSocket client and browser SSE client | [Client](./javascript/client/README.md) |
| `@eleven-am/pondsocket-common` | Shared schema definitions, wire validators, enums and utilities | [Common](./javascript/comnon/README.md) |
| `@eleven-am/pondsocket-express` | Express-owned HTTP server integration | [Express](./javascript/express/README.md) |
| `@eleven-am/pondsocket-nest` | NestJS module, guards, interceptors and decorators | [NestJS](./javascript/nest/README.md) |

The directory name `javascript/comnon` is retained for package-history compatibility; the published package name is `@eleven-am/pondsocket-common`.

## Shared TypeScript schemas

Schemas are optional, but a shared definition lets the server, NestJS adapter, and client type event payloads, replies, presence, assigns, join parameters, and route parameters from one source.

```ts
import {
    definePondChannel,
    definePondSchema,
} from '@eleven-am/pondsocket-common';

interface ChatSchema {
    events: {
        message: { text: string };
        ping: [{ sentAt: number }, { receivedAt: number }];
    };
    presence: { online: boolean };
    assigns: { role: 'admin' | 'member' };
    joinParams: { token: string };
}

export const Chat = definePondChannel(
    definePondSchema<ChatSchema>(),
    '/chat/:roomId',
);
```

See the [core](./javascript/core/README.md), [client](./javascript/client/README.md), and [NestJS](./javascript/nest/README.md) guides for definition-bound examples.

## Multi-node deployment

The TypeScript, Go, Python, and Rust servers can each distribute events through Redis. For TypeScript:

```ts
import { PondSocket, RedisDistributedBackend } from '@eleven-am/pondsocket';

const backend = new RedisDistributedBackend({
    url: process.env.REDIS_URL,
    namespace: 'my-app',
    initializationTimeoutMs: 10_000,
    cleanupTimeoutMs: 5_000,
});

const pond = new PondSocket({ distributedBackend: backend });
await pond.ready();
```

Use a unique namespace for each mutually trusted application. Redis Pub/Sub is a transport, not an authorization boundary: isolate credentials and networks, use TLS where appropriate, and never share a PondSocket namespace with untrusted publishers.

The normative topic and message contract, including the current mixed-runtime limitation, is [DISTRIBUTED_PROTOCOL.md](./DISTRIBUTED_PROTOCOL.md).

## Reliability model

The implementations are designed to make overload and shutdown explicit:

- bounded inbound and outbound queues prevent unlimited memory growth;
- per-connection ordering prevents a later event overtaking an earlier one;
- connection, join, handler, write, and cleanup timeouts bound stalled work;
- slow consumers are disconnected instead of accumulating an unbounded backlog;
- join failures reach a terminal state rather than leaving callers pending;
- graceful close paths release transports, subscriptions, timers, channels, and broker clients;
- origin checks, message-size limits, and configurable hooks provide security and operational controls.

Exact option names and defaults differ by runtime. Use the package README for the implementation you deploy.

## Repository documentation

Each Markdown file has one durable responsibility:

- this README is the cross-runtime project entry point;
- each package README is the landing page published by npm, PyPI, Go documentation, or crates.io;
- [DISTRIBUTED_PROTOCOL.md](./DISTRIBUTED_PROTOCOL.md) is the canonical cross-runtime broker contract;
- `.github/workflows/npm-publish.yml` and `javascript/scripts/publish-packages.mjs` are the executable npm release procedure.

Historical porting checklists and AI-specific instruction files are deliberately not kept as product documentation because they become stale and can contradict the code.

## Development

Run checks from the runtime workspace you changed:

```bash
# TypeScript
cd javascript && npm run validate

# Go
cd go/pondsocket && go test ./...
cd ../pondsocket-client && go test ./...

# Python
cd python && make check

# Rust
cd rust && cargo test --workspace
```

Runtime packages are versioned and released independently. npm publishing is defined by `.github/workflows/npm-publish.yml` and `javascript/scripts/publish-packages.mjs`; keeping the procedure executable avoids a second, drifting Markdown copy.

## License

GPL-3.0. See [LICENSE](./LICENSE).
