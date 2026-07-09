# PondSocket

Typed WebSocket channels for Node.js with presence, private assigns, request/reply events, and Redis-backed distribution.

## Install

```bash
npm install @eleven-am/pondsocket
```

## Server

```ts
import { PondSocket } from '@eleven-am/pondsocket';

const pond = new PondSocket();

const endpoint = pond.createEndpoint('/v1/socket', (ctx) => {
    const token = ctx.query.token;

    if (!token) {
        ctx.decline('Missing token', 401);
        return;
    }

    ctx.accept({ role: 'member' });
});

const chat = endpoint.createChannel('/chat/:roomId', (ctx) => {
    ctx.params.roomId.toUpperCase();
    ctx.accept({ displayName: ctx.joinParams.displayName });
    ctx.trackPresence({ online: true });
});

chat.onEvent('message/:messageId', (ctx) => {
    ctx.params.messageId.toUpperCase();
    ctx.broadcast(ctx.event.event, ctx.payload);
});

pond.listen(3000);
```

Connection and join handlers must call `accept()`, `decline()`, or delegate with `next()`. A handler that completes without deciding is declined instead of leaving the client pending.

## Shared schema

Schemas are optional. A definition can be shared by Core, the Nest adapter, and the client without repeating route literals as generic arguments.

```ts
import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
    PondSocket,
} from '@eleven-am/pondsocket';

interface ChatSchema {
    events: {
        message: { text: string };
        ping: [{ sentAt: number }, { receivedAt: number }];
    };
    presence: {
        online: boolean;
    };
    assigns: {
        role: 'admin' | 'member';
    };
    joinParams: {
        token: string;
    };
}

export const Socket = definePondEndpoint('/v1/socket');
export const Chat = definePondChannel(
    definePondSchema<ChatSchema>(),
    '/chat/:roomId',
);

const pond = new PondSocket();
const endpoint = pond.createEndpoint(Socket, (ctx) => ctx.accept());

const channel = endpoint.createChannel(Chat, (ctx) => {
    ctx.params.roomId.toUpperCase();
    ctx.joinParams.token.toUpperCase();
    ctx.accept({ role: 'member' }).trackPresence({ online: true });
});

channel.onEvent('ping', (ctx) => {
    ctx.payload.sentAt.toFixed();
    ctx.reply('ping', { receivedAt: Date.now() });
});
```

The schema types event names and payloads, request/response tuples, join params, presence, assigns, channel route params, and event route params.

## Distribution

```ts
import { PondSocket, RedisDistributedBackend } from '@eleven-am/pondsocket';

const backend = new RedisDistributedBackend({
    url: process.env.REDIS_URL,
    namespace: 'my-app',
});

const pond = new PondSocket({ distributedBackend: backend });

await pond.ready();
```

Distributed messages use the PondSocket v1 protocol shared by the supported server implementations. Events, presence, assigns, user removal, and cross-node user lookup are propagated through the backend.

## Lifecycle

`ready()` reports distributed-backend initialization failures. `isHealthy()` reports backend/server health. `closeAsync()` stops heartbeat work, closes WebSockets, unsubscribes from the backend, and optionally closes the HTTP server.

When PondSocket uses a framework-owned HTTP server, pass `closeHttpServerOnShutdown: false`.

```ts
const pond = new PondSocket({
    server: existingHttpServer,
    closeHttpServerOnShutdown: false,
});

await pond.closeAsync();
```

Channel patterns use path syntax. `/jobs/:id` and a whole `*` segment are supported; `job:*` is invalid and throws during registration. An unmatched join receives `CHANNEL_NOT_FOUND` with the original channel name and request ID.

## License

GPL-3.0
