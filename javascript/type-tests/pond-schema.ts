import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
    PondSocket,
    ServerActions,
} from '@eleven-am/pondsocket';
import { PondClient } from '@eleven-am/pondsocket-client';

type ChatSchema = {
    events: {
        chat: { text: string };
        ping: [{ n: number }, { ok: boolean }];
        'typed/:eventId': { value: string };
        'lookup/:lookupId': [{ query: string }, { result: string }];
    };
    presence: { userId: string; status: 'online' | 'away' };
    assigns: { role: 'admin' | 'user' };
    joinParams: { token: string };
};

const pond = new PondSocket();
const endpoint = pond.createEndpoint('/socket/:token', (ctx) => {
    ctx.params.token.toUpperCase();
    ctx.accept({ role: 'admin' });
});

const channel = endpoint.createChannel<'/room/:id', ChatSchema>('/room/:id', (ctx) => {
    ctx.params.id.toUpperCase();
    ctx.joinParams.token.toUpperCase();

    // @ts-expect-error presence must match ChatSchema['presence']
    ctx.trackPresence({ wrong: true });

    ctx.trackPresence({ userId: 'u1', status: 'online' });
    ctx.accept({ role: 'user' });
});

channel.onEvent('chat', (ctx) => {
    ctx.payload.text.toUpperCase();
    ctx.user?.assigns.role;
    ctx.reply('ping', { ok: true });

    // @ts-expect-error ping response payload is { ok: boolean }
    ctx.reply('ping', { n: 1 });
});

channel.broadcast('lobby', 'chat', { text: 'ok' });

// @ts-expect-error chat payload must be { text: string }
channel.broadcast('lobby', 'chat', { nope: 1 });

// @ts-expect-error event name must be in ChatSchema['events']
channel.broadcast('lobby', 'missing', {});

const roomChannel = channel.getChannel('/room/1');

roomChannel?.broadcast('chat', { text: 'ok' });

// @ts-expect-error chat payload must be { text: string }
roomChannel?.broadcast('chat', { nope: 1 });

// @ts-expect-error event name must be in ChatSchema['events']
roomChannel?.broadcast('missing', {});

roomChannel?.trackPresence('u1', { userId: 'u1', status: 'online' });

// @ts-expect-error presence must match ChatSchema['presence']
roomChannel?.trackPresence('u1', { wrong: true });

roomChannel?.updateAssigns('u1', { role: 'admin' });

// @ts-expect-error assigns must match ChatSchema['assigns']
roomChannel?.updateAssigns('u1', { role: 'nope' });

const localUser = roomChannel?.getUserData('u1');

localUser?.assigns.role.toUpperCase();
localUser?.presence.status.toUpperCase();

// @ts-expect-error assigns has no such field on ChatSchema['assigns']
localUser?.assigns.missing;

async function checkDistributedUser () {
    const remoteUser = await roomChannel!.getUserAcrossNodes('u1');

    remoteUser?.assigns.role.toUpperCase();
    remoteUser?.presence.status.toUpperCase();

    // @ts-expect-error presence has no such field on ChatSchema['presence']
    remoteUser?.presence.missing;
}

void checkDistributedUser;

roomChannel?.requestUserRemoval('u1');

const client = new PondClient('ws://localhost');
const clientChannel = client.createChannel<ChatSchema>('lobby', { token: 't' });

clientChannel.sendMessage('chat', { text: 'ok' });

// @ts-expect-error chat payload must be { text: string }
clientChannel.sendMessage('chat', { nope: 1 });

// @ts-expect-error event name must be in ChatSchema['events']
clientChannel.sendMessage('missing', {});

clientChannel.onMessageEvent('chat', (message) => {
    message.text.toUpperCase();

    // @ts-expect-error no nope field exists on chat payload
    message.nope;
});

clientChannel.presence.forEach((presence) => {
    presence.userId.toUpperCase();
});

const Chat = definePondChannel(
    definePondSchema<ChatSchema>(),
    '/room/:roomId',
);
const Socket = definePondEndpoint('/socket/:token');

const typedEndpoint = pond.createEndpoint(Socket, (ctx) => {
    ctx.params.token.toUpperCase();
    ctx.accept();
});

const typedChannel = typedEndpoint.createChannel(Chat, (ctx) => {
    ctx.params.roomId.toUpperCase();
    ctx.joinParams.token.toUpperCase();
    ctx.user.assigns.role.toUpperCase();
    ctx.trackPresence({ userId: 'u1', status: 'online' });
    ctx.accept({ role: 'admin' });
});

typedChannel.handleOutgoingEvent('ping', (ctx) => {
    if (ctx.action === ServerActions.BROADCAST) {
        ctx.payload.n.toFixed();
        ctx.transform({ n: ctx.payload.n + 1 });

        // @ts-expect-error broadcast payloads contain n, not ok
        ctx.payload.ok;
    } else {
        ctx.payload.ok.valueOf();
        ctx.transform({ ok: !ctx.payload.ok });

        // @ts-expect-error system reply payloads contain ok, not n
        ctx.payload.n;
    }
});

const definedClientChannel = client.createChannel(Chat, {
    params: { roomId: 'general' },
    joinParams: { token: 'secret' },
});

definedClientChannel.sendMessage('chat', { text: 'typed' });
definedClientChannel.sendForResponse('ping', { n: 1 }).then((response) => response.ok.valueOf());
definedClientChannel.sendMessage('typed/:eventId', { value: 'typed' }, { eventId: 'event-1' });
definedClientChannel.sendForResponse('lookup/:lookupId', { query: 'typed' }, { lookupId: 'lookup-1' })
    .then((response) => response.result.toUpperCase());
definedClientChannel.onMessageEvent('typed/:eventId', (message, event) => {
    message.value.toUpperCase();
    event.params.eventId.toUpperCase();
});
definedClientChannel.onJoin((presence) => presence.status.toUpperCase());
definedClientChannel.onError((error) => error.status.toFixed());
definedClientChannel.joinError?.message.toUpperCase();

// @ts-expect-error route params are required by the channel definition
client.createChannel(Chat, { joinParams: { token: 'secret' } });

// @ts-expect-error join params are required by ChatSchema
client.createChannel(Chat, { params: { roomId: 'general' } });

// @ts-expect-error roomId is the route parameter name
client.createChannel(Chat, { params: { id: 'general' }, joinParams: { token: 'secret' } });

// @ts-expect-error join parameter token must be a string
client.createChannel(Chat, { params: { roomId: 'general' }, joinParams: { token: 1 } });

// @ts-expect-error dynamic events require their route params
definedClientChannel.sendMessage('typed/:eventId', { value: 'typed' });

// @ts-expect-error dynamic response events require their route params
definedClientChannel.sendForResponse('lookup/:lookupId', { query: 'typed' });
