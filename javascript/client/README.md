# PondSocket Client

Browser and Node.js client for PondSocket WebSocket and SSE endpoints.

## Install

```bash
npm install @eleven-am/pondsocket-client @eleven-am/pondsocket-common
```

## Basic use

```ts
import {
    ConnectionState,
    PondClient,
} from '@eleven-am/pondsocket-client';

const client = new PondClient('wss://example.com/v1/socket', {
    token: 'connection-token',
});

client.onConnectionChange((state) => {
    if (state === ConnectionState.CONNECTED) {
        console.log('connected');
    }
});

client.onError(console.error);
client.connect();

const channel = client.createChannel('/chat/general', {
    displayName: 'Ada',
});

channel.onMessageEvent('message', (payload) => console.log(payload));
channel.join();
channel.sendMessage('message', { text: 'hello' });
```

Call `disconnect()` when the client is no longer needed. It cancels reconnect/connection timers, closes all channels, and releases channel subscriptions.

## Shared schema

Use the same channel definition on the server and client:

```ts
import {
    definePondChannel,
    definePondSchema,
} from '@eleven-am/pondsocket-common';
import { PondClient } from '@eleven-am/pondsocket-client';

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

export const Chat = definePondChannel(
    definePondSchema<ChatSchema>(),
    '/chat/:roomId',
);

const client = new PondClient('wss://example.com/v1/socket');
const channel = client.createChannel(Chat, {
    params: { roomId: 'general' },
    joinParams: { token: 'join-token' },
});

channel.onMessageEvent('message', (payload) => {
    payload.text.toUpperCase();
});

const response = await channel.sendForResponse('ping', {
    sentAt: Date.now(),
});

response.receivedAt.toFixed();
channel.join();
```

Route and join parameters are required only when the definition requires them. Schema use itself is optional; `createChannel(name, joinParams)` remains supported.

## Join failures

Join requests have a configurable timeout and always reach a terminal state on decline, timeout, or an unmatched server channel.

```ts
const client = new PondClient('wss://example.com/v1/socket', {}, {
    connectionTimeout: 10_000,
    joinTimeout: 8_000,
    maxReconnectDelay: 30_000,
});

const channel = client.createChannel(Chat, {
    params: { roomId: 'general' },
    joinParams: { token: 'join-token' },
});

channel.onError((error) => {
    console.error(error.code, error.status, error.message);
});

channel.onChannelStateChange(console.log);
channel.join();
```

`joinError` contains the most recent terminal join error. Messages cannot be queued after a channel is declined or closed. A later acknowledgement cannot reopen an explicitly closed channel.

## Presence

`presence`, `onJoin`, `onLeave`, `onPresenceChange`, and `onUsersChange` use the schema's presence type.

## SSE

`SSEClient` has the same channel API and adds `withCredentials` and `getConnectionId()`.

```ts
import { SSEClient } from '@eleven-am/pondsocket-client';

const client = new SSEClient('https://example.com/v1/sse', {}, {
    withCredentials: true,
});
```

## License

GPL-3.0
