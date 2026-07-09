import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
} from '@eleven-am/pondsocket';
import createPondSocket from '@eleven-am/pondsocket-express';
import type { Express } from 'express-serve-static-core';

interface ChatSchema {
    events: {
        message: { text: string };
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

const Socket = definePondEndpoint('/v1/socket/:tenantId');
const Chat = definePondChannel(
    definePondSchema<ChatSchema>(),
    '/chat/:roomId',
);

declare const app: Express;

const server = createPondSocket(app);
const endpoint = server.createEndpoint(Socket, (context) => {
    context.params.tenantId.toUpperCase();
    context.accept();
});

const channel = endpoint.createChannel(Chat, (context) => {
    context.params.roomId.toUpperCase();
    context.joinParams.token.toUpperCase();
    context.accept({ role: 'member' }).trackPresence({ online: true });
});

channel.onEvent('message', (context) => {
    context.payload.text.toUpperCase();
});

// @ts-expect-error endpoint route parameter is tenantId
server.createEndpoint(Socket, (context) => context.params.missing);

// @ts-expect-error event payload must match ChatSchema['events']['message']
channel.broadcast('/chat/general', 'message', { missing: true });
