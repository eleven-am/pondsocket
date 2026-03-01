import { createServer, Server } from 'http';
import { AddressInfo } from 'net';

import { ChannelState } from '@eleven-am/pondsocket-common';

import { PondSocket } from '../server/server';
import { PondClient } from '../../../client/src/node/node';
import { ConnectionState } from '../../../client/src/types';

function waitForState(client: PondClient, target: ConnectionState, timeoutMs = 5000): Promise<void> {
    return new Promise((resolve, reject) => {
        if (client.getState() === target) {
            return resolve();
        }
        const timer = setTimeout(() => {
            unsub();
            reject(new Error(`Timed out waiting for state ${target}, current: ${client.getState()}`));
        }, timeoutMs);
        const unsub = client.onConnectionChange((state) => {
            if (state === target) {
                clearTimeout(timer);
                unsub();
                resolve();
            }
        });
    });
}

function waitForChannelState(
    channel: ReturnType<PondClient['createChannel']>,
    target: ChannelState,
    timeoutMs = 5000,
): Promise<void> {
    return new Promise((resolve, reject) => {
        if (channel.channelState === target) {
            return resolve();
        }
        const timer = setTimeout(() => {
            unsub();
            reject(new Error(`Timed out waiting for channel state ${target}, current: ${channel.channelState}`));
        }, timeoutMs);
        const unsub = channel.onChannelStateChange((state) => {
            if (state === target) {
                clearTimeout(timer);
                unsub();
                resolve();
            }
        });
    });
}

function delay(ms: number): Promise<void> {
    return new Promise((resolve) => setTimeout(resolve, ms));
}

describe('Integration Tests', () => {
    let server: PondSocket;
    let httpServer: Server;
    let port: number;
    let clients: PondClient[];

    function createClient(path = '/ws', params: Record<string, any> = {}): PondClient {
        const client = new PondClient(`ws://localhost:${port}${path}`, params, {
            connectionTimeout: 5000,
            maxReconnectDelay: 500,
        });
        clients.push(client);
        return client;
    }

    beforeEach((done) => {
        clients = [];
        httpServer = createServer();
        server = new PondSocket({
            server: httpServer,
            heartbeatInterval: 60000,
        });
        httpServer.listen(0, () => {
            port = (httpServer.address() as AddressInfo).port;
            done();
        });
    });

    afterEach((done) => {
        clients.forEach((c) => {
            try { c.disconnect(); } catch (_e) { /* ignore */ }
        });
        clients = [];
        server.close(() => done(), 1000);
    });

    describe('connection', () => {
        it('should connect a client to a server endpoint', async () => {
            server.createEndpoint('/ws', (ctx, next) => {
                ctx.accept();
            });

            const client = createClient();
            client.connect();

            await waitForState(client, ConnectionState.CONNECTED);
            expect(client.getState()).toBe(ConnectionState.CONNECTED);
        });

        it('should support query parameters on connection', async () => {
            let receivedQuery: Record<string, string> = {};

            server.createEndpoint('/ws', (ctx, next) => {
                receivedQuery = ctx.query;
                ctx.accept();
            });

            const client = createClient('/ws', { token: 'abc123', role: 'admin' });
            client.connect();

            await waitForState(client, ConnectionState.CONNECTED);
            expect(receivedQuery.token).toBe('abc123');
            expect(receivedQuery.role).toBe('admin');
        });
    });

    describe('channel join', () => {
        it('should join a channel successfully', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/general');
            channel.join();

            await waitForChannelState(channel, ChannelState.JOINED);
            expect(channel.channelState).toBe(ChannelState.JOINED);
        });

        it('should pass join params to the server', async () => {
            let receivedJoinParams: Record<string, any> = {};

            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            endpoint.createChannel('/chat/:room', (ctx) => {
                receivedJoinParams = ctx.joinParams;
                ctx.accept();
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/general', { username: 'alice', color: 'blue' });
            channel.join();

            await waitForChannelState(channel, ChannelState.JOINED);
            expect(receivedJoinParams.username).toBe('alice');
            expect(receivedJoinParams.color).toBe('blue');
        });

        it('should decline a client from joining a channel', async () => {
            let declineCalled = false;

            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            endpoint.createChannel('/chat/:room', (ctx) => {
                declineCalled = true;
                ctx.decline('Not allowed', 403);
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/private');
            channel.join();

            await delay(500);
            expect(declineCalled).toBe(true);
            expect(channel.channelState).not.toBe(ChannelState.JOINED);
        });
    });

    describe('messaging', () => {
        it('should send a message from client and receive broadcast back', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            pondChannel.onEvent('/greet', (ctx) => {
                ctx.broadcast('greeting', { text: `Hello from ${ctx.event.payload.name}` });
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/lobby');
            channel.join();
            await waitForChannelState(channel, ChannelState.JOINED);

            const messagePromise = new Promise<{ event: string; payload: any }>((resolve) => {
                channel.onMessage((event, payload) => {
                    if (event === 'greeting') {
                        resolve({ event, payload });
                    }
                });
            });

            channel.sendMessage('/greet', { name: 'Alice' });

            const received = await messagePromise;
            expect(received.event).toBe('greeting');
            expect(received.payload.text).toBe('Hello from Alice');
        });

        it('should broadcast to multiple clients in the same channel', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            pondChannel.onEvent('/ping', (ctx) => {
                ctx.broadcast('pong', { from: ctx.event.payload.sender });
            });

            const client1 = createClient();
            const client2 = createClient();
            client1.connect();
            client2.connect();
            await Promise.all([
                waitForState(client1, ConnectionState.CONNECTED),
                waitForState(client2, ConnectionState.CONNECTED),
            ]);

            const ch1 = client1.createChannel('/chat/room1');
            const ch2 = client2.createChannel('/chat/room1');
            ch1.join();
            ch2.join();
            await Promise.all([
                waitForChannelState(ch1, ChannelState.JOINED),
                waitForChannelState(ch2, ChannelState.JOINED),
            ]);

            const msg1Promise = new Promise<any>((resolve) => {
                ch1.onMessage((event, payload) => {
                    if (event === 'pong') resolve(payload);
                });
            });
            const msg2Promise = new Promise<any>((resolve) => {
                ch2.onMessage((event, payload) => {
                    if (event === 'pong') resolve(payload);
                });
            });

            ch1.sendMessage('/ping', { sender: 'client1' });

            const [msg1, msg2] = await Promise.all([msg1Promise, msg2Promise]);
            expect(msg1.from).toBe('client1');
            expect(msg2.from).toBe('client1');
        });

        it('should broadcastFrom to exclude the sender', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            pondChannel.onEvent('/announce', (ctx) => {
                ctx.broadcastFrom('announcement', { text: ctx.event.payload.text });
            });

            const client1 = createClient();
            const client2 = createClient();
            client1.connect();
            client2.connect();
            await Promise.all([
                waitForState(client1, ConnectionState.CONNECTED),
                waitForState(client2, ConnectionState.CONNECTED),
            ]);

            const ch1 = client1.createChannel('/chat/room1');
            const ch2 = client2.createChannel('/chat/room1');
            ch1.join();
            ch2.join();
            await Promise.all([
                waitForChannelState(ch1, ChannelState.JOINED),
                waitForChannelState(ch2, ChannelState.JOINED),
            ]);

            let client1Received = false;
            ch1.onMessage((event) => {
                if (event === 'announcement') client1Received = true;
            });

            const msg2Promise = new Promise<any>((resolve) => {
                ch2.onMessage((event, payload) => {
                    if (event === 'announcement') resolve(payload);
                });
            });

            ch1.sendMessage('/announce', { text: 'hello everyone' });

            const received = await msg2Promise;
            expect(received.text).toBe('hello everyone');

            await delay(200);
            expect(client1Received).toBe(false);
        });
    });

    describe('presence', () => {
        it('should track presence when a user joins', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
                ctx.trackPresence({ username: ctx.joinParams.username as string, status: 'online' });
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/lobby', { username: 'alice' });

            const joinPromise = new Promise<any>((resolve) => {
                channel.onJoin((presence) => {
                    resolve(presence);
                });
            });

            channel.join();
            await waitForChannelState(channel, ChannelState.JOINED);

            const presence = await joinPromise;
            expect(presence.username).toBe('alice');
            expect(presence.status).toBe('online');
        });

        it('should notify when a second user joins', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
                ctx.trackPresence({ username: ctx.joinParams.username as string });
            });

            const client1 = createClient();
            const client2 = createClient();
            client1.connect();
            client2.connect();
            await Promise.all([
                waitForState(client1, ConnectionState.CONNECTED),
                waitForState(client2, ConnectionState.CONNECTED),
            ]);

            const ch1 = client1.createChannel('/chat/room1', { username: 'alice' });
            ch1.join();
            await waitForChannelState(ch1, ChannelState.JOINED);

            const joinPromise = new Promise<any>((resolve) => {
                ch1.onJoin((presence) => {
                    if (presence.username === 'bob') {
                        resolve(presence);
                    }
                });
            });

            const ch2 = client2.createChannel('/chat/room1', { username: 'bob' });
            ch2.join();
            await waitForChannelState(ch2, ChannelState.JOINED);

            const joinedPresence = await joinPromise;
            expect(joinedPresence.username).toBe('bob');
        });

        it('should update presence and notify other clients', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
                ctx.trackPresence({ username: ctx.joinParams.username as string, status: 'online' });
            });

            pondChannel.onEvent('/update-status', (ctx) => {
                ctx.updatePresence({ username: ctx.event.payload.username, status: ctx.event.payload.status });
            });

            const client1 = createClient();
            const client2 = createClient();
            client1.connect();
            client2.connect();
            await Promise.all([
                waitForState(client1, ConnectionState.CONNECTED),
                waitForState(client2, ConnectionState.CONNECTED),
            ]);

            const ch1 = client1.createChannel('/chat/room1', { username: 'alice' });
            const ch2 = client2.createChannel('/chat/room1', { username: 'bob' });
            ch1.join();
            await waitForChannelState(ch1, ChannelState.JOINED);
            ch2.join();
            await waitForChannelState(ch2, ChannelState.JOINED);

            const presenceChangePromise = new Promise<any>((resolve) => {
                ch2.onPresenceChange((payload) => {
                    resolve(payload);
                });
            });

            ch1.sendMessage('/update-status', { username: 'alice', status: 'away' });

            const presencePayload = await presenceChangePromise;
            const alicePresence = presencePayload.presence.find((p: any) => p.username === 'alice');
            expect(alicePresence.status).toBe('away');
        });

        it('should detect user leave via presence', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
                ctx.trackPresence({ username: ctx.joinParams.username as string });
            });

            const client1 = createClient();
            const client2 = createClient();
            client1.connect();
            client2.connect();
            await Promise.all([
                waitForState(client1, ConnectionState.CONNECTED),
                waitForState(client2, ConnectionState.CONNECTED),
            ]);

            const ch1 = client1.createChannel('/chat/room1', { username: 'alice' });
            const ch2 = client2.createChannel('/chat/room1', { username: 'bob' });
            ch1.join();
            await waitForChannelState(ch1, ChannelState.JOINED);

            const joinPromise = new Promise<void>((resolve) => {
                ch1.onJoin((p) => {
                    if (p.username === 'bob') resolve();
                });
            });
            ch2.join();
            await waitForChannelState(ch2, ChannelState.JOINED);
            await joinPromise;

            const leavePromise = new Promise<any>((resolve) => {
                ch1.onLeave((presence) => {
                    if (presence.username === 'bob') {
                        resolve(presence);
                    }
                });
            });

            client2.disconnect();

            const leftPresence = await leavePromise;
            expect(leftPresence.username).toBe('bob');
        });
    });

    describe('channel leave', () => {
        it('should leave a channel and trigger onLeave callback', async () => {
            let leaveTriggered = false;
            let leftUserId = '';

            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            pondChannel.onLeave((event) => {
                leaveTriggered = true;
                leftUserId = event.user.id;
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/lobby');
            channel.join();
            await waitForChannelState(channel, ChannelState.JOINED);

            channel.leave();

            await delay(300);
            expect(leaveTriggered).toBe(true);
            expect(leftUserId).toBeTruthy();
        });
    });

    describe('disconnect and reconnect', () => {
        it('should reconnect automatically after disconnect', async () => {
            server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            (client as any)._socket?.close();

            await waitForState(client, ConnectionState.DISCONNECTED);
            await waitForState(client, ConnectionState.CONNECTED, 10000);
            expect(client.getState()).toBe(ConnectionState.CONNECTED);
        });

        it('should rejoin a channel after reconnect', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/lobby');
            channel.join();
            await waitForChannelState(channel, ChannelState.JOINED);

            (client as any)._socket?.close();

            await waitForChannelState(channel, ChannelState.STALLED);
            await waitForState(client, ConnectionState.CONNECTED, 10000);
            await waitForChannelState(channel, ChannelState.JOINED, 10000);

            expect(channel.channelState).toBe(ChannelState.JOINED);
        });
    });

    describe('server-side reply', () => {
        it('should send a reply from event handler', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            pondChannel.onEvent('/echo', (ctx) => {
                ctx.reply('echo-response', { echo: ctx.event.payload.message });
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const channel = client.createChannel('/chat/lobby');
            channel.join();
            await waitForChannelState(channel, ChannelState.JOINED);

            const replyPromise = new Promise<any>((resolve) => {
                channel.onMessage((event, payload) => {
                    if (event === 'echo-response') {
                        resolve(payload);
                    }
                });
            });

            channel.sendMessage('/echo', { message: 'hello world' });

            const reply = await replyPromise;
            expect(reply.echo).toBe('hello world');
        });
    });

    describe('multiple channels', () => {
        it('should handle a client in multiple channels', async () => {
            const endpoint = server.createEndpoint('/ws', (ctx) => {
                ctx.accept();
            });

            const pondChannel = endpoint.createChannel('/chat/:room', (ctx) => {
                ctx.accept();
            });

            pondChannel.onEvent('/msg', (ctx) => {
                ctx.broadcast('message', { text: ctx.event.payload.text, channel: ctx.channelName });
            });

            const client = createClient();
            client.connect();
            await waitForState(client, ConnectionState.CONNECTED);

            const ch1 = client.createChannel('/chat/room1');
            const ch2 = client.createChannel('/chat/room2');
            ch1.join();
            ch2.join();
            await Promise.all([
                waitForChannelState(ch1, ChannelState.JOINED),
                waitForChannelState(ch2, ChannelState.JOINED),
            ]);

            const msg1Promise = new Promise<any>((resolve) => {
                ch1.onMessage((event, payload) => {
                    if (event === 'message') resolve(payload);
                });
            });
            const msg2Promise = new Promise<any>((resolve) => {
                ch2.onMessage((event, payload) => {
                    if (event === 'message') resolve(payload);
                });
            });

            ch1.sendMessage('/msg', { text: 'hello room1' });
            ch2.sendMessage('/msg', { text: 'hello room2' });

            const [msg1, msg2] = await Promise.all([msg1Promise, msg2Promise]);
            expect(msg1.text).toBe('hello room1');
            expect(msg2.text).toBe('hello room2');
        });
    });
});
