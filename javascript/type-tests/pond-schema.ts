import { PondSocket } from '@eleven-am/pondsocket';
import { PondClient } from '@eleven-am/pondsocket-client';

type ChatSchema = {
    events: {
        chat: { text: string };
        ping: [{ n: number }, { ok: boolean }];
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
    ctx.reply('ping', { n: 1 });

    // @ts-expect-error ping request payload is { n: number }
    ctx.reply('ping', { ok: true });
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
