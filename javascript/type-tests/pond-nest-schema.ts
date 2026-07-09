import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
    ServerActions,
} from '@eleven-am/pondsocket';
import type {
    PondConnectionContext,
    PondEventContext,
    PondEventPayload,
    PondEventHandler,
    PondJoinContext,
    PondOutgoingContext,
    PondResponseFor,
    PondSchemaAssigns,
    PondSchemaPresence,
} from '@eleven-am/pondsocket-nest';

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

const Chat = definePondChannel(
    definePondSchema<ChatSchema>(),
    '/chat/:roomId',
);
const Socket = definePondEndpoint('/socket/:token');

class SocketEndpoint {
    @Socket.OnConnection()
    connect (@Socket.GetContext() context: PondConnectionContext<typeof Socket>) {
        context.params.token.toUpperCase();
        context.accept();

        // @ts-expect-error connection contexts cannot track channel presence
        context.trackPresence({ online: true });
    }
}

class ChatController {
    @Chat.OnJoin()
    join (@Chat.GetContext() context: PondJoinContext<typeof Chat>) {
        context.params.roomId.toUpperCase();
        context.joinParams.token.toUpperCase();

        // @ts-expect-error join contexts do not carry an event payload
        context.payload;

        // @ts-expect-error presence updates are event operations
        context.updatePresence({ online: false });
    }

    @Chat.OnEvent('message/:messageId')
    message (
        @Chat.GetContext()
        context: PondEventContext<typeof Chat, 'message/:messageId'>,
    ) {
        context.params.messageId.toUpperCase();
        context.payload.text.toUpperCase();
        context.reply({ text: 'received' });

        // @ts-expect-error event contexts cannot accept a channel join
        context.accept();
    }
}

Chat.OnEvent('message/:messageId');

// @ts-expect-error handlers must use an event declared by ChatSchema
Chat.OnEvent('missing');

declare const joinContext: PondJoinContext<typeof Chat>;

joinContext.params.roomId.toUpperCase();
joinContext.joinParams.token.toUpperCase();
joinContext.user.assigns.role.toUpperCase();
joinContext.trackPresence({ online: true });

// @ts-expect-error presence is typed by ChatSchema
joinContext.trackPresence({ status: 'online' });

declare const eventContext: PondEventContext<typeof Chat, 'message/:messageId'>;

eventContext.params.messageId.toUpperCase();
eventContext.payload.text.toUpperCase();
eventContext.user.presence.online.valueOf();
eventContext.assign({ role: 'admin' });
eventContext.broadcast(eventContext.event.event, { text: 'saved' });
eventContext.reply({ text: 'received' });

declare const pingContext: PondEventContext<typeof Chat, 'ping'>;

pingContext.reply({ receivedAt: Date.now() });

// @ts-expect-error ping replies contain receivedAt, not sentAt
pingContext.reply({ sentAt: Date.now() });

// @ts-expect-error event route parameter is messageId
eventContext.params.roomId;

// @ts-expect-error assigns role is a closed union
eventContext.assign({ role: 'owner' });

// @ts-expect-error context broadcasts preserve the established event-and-payload contract
eventContext.broadcast('message/:messageId', { text: 'saved' }, { messageId: 'message-1' });

declare const outgoingContext: PondOutgoingContext<typeof Chat, 'message/:messageId'>;

outgoingContext.transform({ text: 'filtered' });
outgoingContext.event.payload.text.toUpperCase();

// @ts-expect-error outgoing contexts cannot reply to incoming requests
outgoingContext.reply({ text: 'filtered' });

// @ts-expect-error outgoing payload is typed by the selected event
outgoingContext.transform({ message: 'filtered' });

declare const outgoingPingContext: PondOutgoingContext<typeof Chat, 'ping'>;

if (outgoingPingContext.action === ServerActions.BROADCAST) {
    outgoingPingContext.payload.sentAt.toFixed();
    outgoingPingContext.event.payload.sentAt.toFixed();
    outgoingPingContext.transform({ sentAt: Date.now() });

    // @ts-expect-error broadcast payloads contain sentAt, not receivedAt
    outgoingPingContext.payload.receivedAt;
} else {
    outgoingPingContext.payload.receivedAt.toFixed();
    outgoingPingContext.event.payload.receivedAt.toFixed();
    outgoingPingContext.transform({ receivedAt: Date.now() });

    // @ts-expect-error system reply payloads contain receivedAt, not sentAt
    outgoingPingContext.payload.sentAt;
}

const payload: PondEventPayload<typeof Chat, 'ping'> = { sentAt: Date.now() };
const presence: PondSchemaPresence<typeof Chat> = { online: true };
const assigns: PondSchemaAssigns<typeof Chat> = { role: 'member' };
const response: PondResponseFor<typeof Chat, 'ping'> = {
    event: 'ping',
    payload: { receivedAt: Date.now() },
};
const dynamicResponse: PondResponseFor<typeof Chat, 'message/:messageId'> = {
    event: 'message/:messageId',
    eventParams: { messageId: 'message-1' },
    payload: { text: 'saved' },
};
const handler: PondEventHandler<typeof Chat, 'ping'> = (context) => ({
    event: 'ping',
    payload: { receivedAt: context.payload.sentAt },
});

// @ts-expect-error ping responses contain receivedAt, not sentAt
const invalidResponse: PondResponseFor<typeof Chat, 'ping'> = { event: 'ping', payload: { sentAt: 1 } };

void payload;
void presence;
void assigns;
void response;
void dynamicResponse;
void handler;
void invalidResponse;
void ChatController;
void SocketEndpoint;
