# PondSocket NestJS

NestJS integration for PondSocket endpoints and channels.

## Install

```bash
npm install @eleven-am/pondsocket @eleven-am/pondsocket-common @eleven-am/pondsocket-nest
```

`@nestjs/common`, `@nestjs/core`, and `rxjs` are peer dependencies.

## Shared schema

Define a channel once in code shared by the server and client. Schema use is optional; the legacy string decorators and channel APIs remain supported.

```ts
import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
} from '@eleven-am/pondsocket-common';

interface ChatSchema {
    events: {
        'message/:messageId': { text: string };
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
```

The optional second argument to `definePondChannel` is the runtime channel pattern. It is inferred as a literal type, so it is never repeated as a generic.

## Nest handlers

Endpoint association uses the endpoint class. If an application has exactly one endpoint, `{ endpoint: SocketEndpoint }` may be omitted.

```ts
import type {
    PondConnectionContext,
    PondEventContext,
    PondJoinContext,
    PondOutgoingContext,
} from '@eleven-am/pondsocket-nest';
import { Chat, Socket } from './pond-definitions';

@Socket.Endpoint()
export class SocketEndpoint {
    @Socket.OnConnection()
    connect(@Socket.GetContext() ctx: PondConnectionContext<typeof Socket>) {
        ctx.accept();
    }
}

@Chat.Channel({ endpoint: SocketEndpoint })
export class ChatController {
    @Chat.OnJoin()
    join(@Chat.GetContext() ctx: PondJoinContext<typeof Chat>) {
        ctx.joinParams.token.toUpperCase();
        ctx.params.roomId.toUpperCase();
        ctx.accept({ role: 'member' }).trackPresence({ online: true });
    }

    @Chat.OnEvent('message/:messageId')
    message(
        @Chat.GetContext()
        ctx: PondEventContext<typeof Chat, 'message/:messageId'>,
    ) {
        ctx.params.messageId.toUpperCase();
        ctx.payload.text.toUpperCase();
        ctx.broadcast(ctx.event.event, ctx.payload);
    }

    @Chat.OnEvent('ping')
    ping(
        @Chat.GetContext()
        ctx: PondEventContext<typeof Chat, 'ping'>,
    ) {
        ctx.reply({ receivedAt: Date.now() });
    }

    @Chat.OnOutgoingEvent('message/:messageId')
    outgoing(
        @Chat.GetContext()
        ctx: PondOutgoingContext<typeof Chat, 'message/:messageId'>,
    ) {
        ctx.transform({ text: ctx.payload.text.trim() });
    }
}
```

Every handler parameter must use a PondSocket parameter decorator. `@Chat.GetContext()` and `@Socket.GetContext()` explicitly inject the definition-bound context; the TypeScript annotation narrows it to the handler lifecycle and event. Extraction decorators such as `@GetEventPayload()`, `@GetEventParams()`, and custom decorators remain available for individual values.

Outgoing handlers run once per recipient. They must call `ctx.transform(payload)` or `ctx.block()` explicitly; return values are ignored. For request/response tuple events, `ctx.action` discriminates the payload: `BROADCAST` carries the request payload and `SYSTEM` carries the response payload.

## Module

```ts
import { Module } from '@nestjs/common';
import { PondSocketModule } from '@eleven-am/pondsocket-nest';

@Module({
    imports: [
        PondSocketModule.forRoot({
            providers: [SocketEndpoint, ChatController],
            isExclusiveSocketServer: false,
        }),
    ],
})
export class AppModule {}
```

`forRootAsync` accepts the same Nest module metadata plus `inject` and `useFactory`. Its factory can return `backend`, `maxMessageSize`, and `heartbeatInterval`.

## Client

```ts
import { PondClient } from '@eleven-am/pondsocket-client';
import { Chat } from './pond-definitions';

const client = new PondClient('wss://example.com/v1/socket');
const channel = client.createChannel(Chat, {
    params: { roomId: 'general' },
    joinParams: { token: 'secret' },
});

channel.onMessageEvent('message/:messageId', (payload) => {
    payload.text.toUpperCase();
});

channel.onError((error) => console.error(error.code, error.status, error.message));
channel.join();
```

Required route and join parameters are derived from the definition. Presence, assigns, event names, event payloads, and request/response tuples are derived from the same schema.

## Responses and errors

A handler can use the context directly or return a declarative response. The nested `payload` form avoids collisions with control fields:

```ts
return {
    event: 'message/:messageId',
    eventParams: { messageId: 'message-1' },
    payload: { text: 'saved' },
    assigns: { role: 'member' },
    presence: { online: true },
};
```

Connection and join handlers can decline declaratively:

```ts
return { decline: { message: 'Invalid token', status: 401 } };
```

Unhandled exceptions are logged through Nest's `Logger`. Event failures reply with `INTERNAL_SERVER_ERROR`; rejected event guards reply with `UNAUTHORIZED_BROADCAST`.

## Operations

`PondSocketService` is exported and injectable. `pondSocket` exposes the underlying server and `isHealthy()` reports backend/server health. Module initialization waits for the distributed backend, and module shutdown awaits PondSocket cleanup without closing Nest's shared HTTP server.

```ts
constructor(private readonly sockets: PondSocketService) {}

health() {
    return this.sockets.isHealthy();
}
```

Channel patterns use path syntax: `/jobs/:id` and whole-segment `*` are supported. Patterns such as `job:*` are rejected at startup. A valid join that matches no registered channel receives a correlated `CHANNEL_NOT_FOUND` error instead of remaining in `JOINING`.

## Legacy API

`@Endpoint(path)`, `@Channel(path)`, `@OnConnectionRequest()`, `@OnJoinRequest()`, `@OnEvent(event)`, `@OnOutgoingEvent(event)`, and `@OnLeave()` remain available. In applications with multiple endpoints, shared definitions are recommended because they provide explicit endpoint ownership and schema-derived types.
