# PondSocket Express

Express adapter for `@eleven-am/pondsocket`. It creates one HTTP server for an existing Express application and attaches PondSocket to that same server.

## Install

```bash
npm install express @eleven-am/pondsocket @eleven-am/pondsocket-express
```

## Use

```ts
import express from 'express';
import createPondSocket from '@eleven-am/pondsocket-express';

const app = express();
app.get('/health', (_request, response) => response.send('ok'));

const realtime = createPondSocket(app);

const endpoint = realtime.createEndpoint('/v1/socket', (ctx) => {
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

realtime.listen(3000);
```

The returned object contains:

- `app`: the original Express application;
- `server`: the shared Node.js HTTP server;
- `pond`: the underlying `PondSocket` instance;
- `createEndpoint`: the bound PondSocket endpoint factory;
- `listen`: the shared server's bound `listen` method;
- `close`: closes PondSocket first, then the HTTP server.

All options accepted by `PondSocket` except `server` may be passed as the second argument. The adapter owns the HTTP server it creates and configures PondSocket to close that server during shutdown.

## Client

Use the named `PondClient` export:

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

## Redis distribution

```ts
import express from 'express';
import { RedisDistributedBackend } from '@eleven-am/pondsocket';
import createPondSocket from '@eleven-am/pondsocket-express';

const backend = new RedisDistributedBackend({
    url: process.env.REDIS_URL,
    namespace: 'my-app',
    initializationTimeoutMs: 10_000,
    cleanupTimeoutMs: 5_000,
});

const realtime = createPondSocket(express(), {
    distributedBackend: backend,
});

await realtime.pond.ready();
realtime.listen(3000);
```

Use a namespace and Redis deployment shared only by mutually trusted application nodes. Redis Pub/Sub does not authenticate individual PondSocket messages.

## Shutdown

`close()` starts asynchronous PondSocket cleanup and closes the shared HTTP server. For code that must await complete cleanup directly, use the underlying instance:

```ts
await realtime.pond.closeAsync();
```

The core server's queue limits, timeouts, origin policy, health checks, and lifecycle options are documented in [`@eleven-am/pondsocket`](../core/README.md).

## License

GPL-3.0
