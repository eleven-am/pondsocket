import { ChannelReceiver } from '@eleven-am/pondsocket-common';

import { RedisDistributedBackend } from '../abstracts/distributor';
import { DistributedChannelMessage, DistributedMessageType } from '../types';

jest.mock('redis', () => {
    const channels = new Map<string, Set<(message: string) => void>>();

    const makeClient = () => {
        const own = new Map<string, (message: string) => void>();

        return {
            isOpen: true,
            isReady: true,
            connect: async () => {},
            duplicate: () => makeClient(),
            on: () => {},
            subscribe: async (key: string, listener: (message: string) => void) => {
                if (!channels.has(key)) {
                    channels.set(key, new Set());
                }
                channels.get(key)!.add(listener);
                own.set(key, listener);
            },
            unsubscribe: async (key: string) => {
                const listener = own.get(key);

                if (listener) {
                    channels.get(key)?.delete(listener);
                }
                own.delete(key);
            },
            publish: async (key: string, message: string) => {
                const subscribers = channels.get(key);

                if (subscribers) {
                    [...subscribers].forEach((listener) => listener(message));
                }

                return 0;
            },
            quit: async () => {
                for (const [key, listener] of own) {
                    channels.get(key)?.delete(listener);
                }
                own.clear();
            },
        };
    };

    return {
        __esModule: true,
        createClient: () => makeClient(),
    };
});

const ENDPOINT = '/api/socket';
const CHANNEL = '/chat/1';

describe('RedisDistributedBackend transport', () => {
    let nodeA: RedisDistributedBackend;
    let nodeB: RedisDistributedBackend;

    beforeEach(async () => {
        nodeA = new RedisDistributedBackend({ namespace: 'test-ns' });
        nodeB = new RedisDistributedBackend({ namespace: 'test-ns' });
        await nodeA.initialize();
        await nodeB.initialize();
    });

    afterEach(async () => {
        await nodeA.cleanup();
        await nodeB.cleanup();
    });

    const roundTrip = async (message: DistributedChannelMessage) => {
        const received: DistributedChannelMessage[] = [];
        const selfReceived: DistributedChannelMessage[] = [];

        await nodeB.subscribeToChannel(ENDPOINT, CHANNEL, (m) => received.push(m));
        await nodeA.subscribeToChannel(ENDPOINT, CHANNEL, (m) => selfReceived.push(m));

        await nodeA.broadcast(ENDPOINT, CHANNEL, message);

        return {
            received,
            selfReceived,
        };
    };

    const expectEnvelope = (message: any, type: DistributedMessageType) => {
        expect(message.protocol).toBe('pondsocket.distributed');
        expect(message.version).toBe(1);
        expect(message.type).toBe(type);
        expect(typeof message.messageId).toBe('string');
        expect(typeof message.timestamp).toBe('number');
        expect(message.sourceNodeId).toBe(nodeA.nodeId);
        expect(message.endpointName).toBe('api/socket');
        expect(message.channelName).toBe(CHANNEL);
    };

    it('drops messages that originate from the local node (self-filter)', async () => {
        const { received, selfReceived } = await roundTrip({
            type: DistributedMessageType.USER_LEFT,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
        });

        expect(received).toHaveLength(1);
        expect(selfReceived).toHaveLength(0);
    });

    it('round-trips USER_MESSAGE preserving camelCase keys', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.USER_MESSAGE,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            fromUserId: 'user1',
            event: 'chat',
            payload: { text: 'hi' },
            requestId: 'req-1',
            recipientDescriptor: ChannelReceiver.ALL_USERS,
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.USER_MESSAGE);
        expect(msg.fromUserId).toBe('user1');
        expect(msg.event).toBe('chat');
        expect(msg.payload).toEqual({ text: 'hi' });
        expect(msg.requestId).toBe('req-1');
        expect(msg.recipientDescriptor).toBe(ChannelReceiver.ALL_USERS);
    });

    it('round-trips PRESENCE_UPDATE with no event field', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.PRESENCE_UPDATE,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
            presence: { status: 'online' },
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.PRESENCE_UPDATE);
        expect(msg.userId).toBe('user1');
        expect(msg.presence).toEqual({ status: 'online' });
        expect(msg).not.toHaveProperty('event');
    });

    it('round-trips PRESENCE_REMOVED', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.PRESENCE_REMOVED,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.PRESENCE_REMOVED);
        expect(msg.userId).toBe('user1');
        expect(msg).not.toHaveProperty('event');
    });

    it('round-trips ASSIGNS_UPDATE with a full assigns snapshot', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.ASSIGNS_UPDATE,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
            assigns: { role: 'admin' },
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.ASSIGNS_UPDATE);
        expect(msg.userId).toBe('user1');
        expect(msg.assigns).toEqual({ role: 'admin' });
    });

    it('round-trips EVICT_USER', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.EVICT_USER,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
            reason: 'spam',
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.EVICT_USER);
        expect(msg.userId).toBe('user1');
        expect(msg.reason).toBe('spam');
    });

    it('round-trips USER_REMOVE', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.USER_REMOVE,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.USER_REMOVE);
        expect(msg.userId).toBe('user1');
    });

    it('round-trips USER_GET_REQUEST', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.USER_GET_REQUEST,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
            requestId: 'lookup-1',
            fromNode: 'node-a',
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.USER_GET_REQUEST);
        expect(msg.userId).toBe('user1');
        expect(msg.requestId).toBe('lookup-1');
        expect(msg.fromNode).toBe('node-a');
    });

    it('round-trips USER_GET_RESPONSE', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.USER_GET_RESPONSE,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
            requestId: 'lookup-1',
            assigns: { role: 'admin' },
            presence: { status: 'online' },
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.USER_GET_RESPONSE);
        expect(msg.userId).toBe('user1');
        expect(msg.requestId).toBe('lookup-1');
        expect(msg.assigns).toEqual({ role: 'admin' });
        expect(msg.presence).toEqual({ status: 'online' });
    });

    it('round-trips USER_JOINED', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.USER_JOINED,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
            assigns: { role: 'admin' },
            presence: { status: 'online' },
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.USER_JOINED);
        expect(msg.userId).toBe('user1');
        expect(msg.assigns).toEqual({ role: 'admin' });
        expect(msg.presence).toEqual({ status: 'online' });
    });

    it('round-trips USER_LEFT', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.USER_LEFT,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.USER_LEFT);
        expect(msg.userId).toBe('user1');
    });

    it('round-trips STATE_REQUEST', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.STATE_REQUEST,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            fromNode: 'node-a',
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.STATE_REQUEST);
        expect(msg.fromNode).toBe('node-a');
    });

    it('round-trips STATE_RESPONSE with id-keyed user entries', async () => {
        const { received } = await roundTrip({
            type: DistributedMessageType.STATE_RESPONSE,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            users: [
                { id: 'user1', assigns: { role: 'admin' }, presence: { status: 'online' } },
            ],
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.STATE_RESPONSE);
        expect(msg.users).toHaveLength(1);
        expect(msg.users[0].id).toBe('user1');
        expect(msg.users[0].assigns).toEqual({ role: 'admin' });
        expect(msg.users[0].presence).toEqual({ status: 'online' });
        expect(msg.users[0]).not.toHaveProperty('userId');
    });

    describe('recipientDescriptor', () => {
        it('preserves ALL_USERS', async () => {
            const { received } = await roundTrip({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: ENDPOINT,
                channelName: CHANNEL,
                fromUserId: 'user1',
                event: 'e',
                payload: {},
                requestId: 'r',
                recipientDescriptor: ChannelReceiver.ALL_USERS,
            });

            expect((received[0] as any).recipientDescriptor).toBe(ChannelReceiver.ALL_USERS);
        });

        it('preserves ALL_EXCEPT_SENDER', async () => {
            const { received } = await roundTrip({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: ENDPOINT,
                channelName: CHANNEL,
                fromUserId: 'user1',
                event: 'e',
                payload: {},
                requestId: 'r',
                recipientDescriptor: ChannelReceiver.ALL_EXCEPT_SENDER,
            });

            expect((received[0] as any).recipientDescriptor).toBe(ChannelReceiver.ALL_EXCEPT_SENDER);
        });

        it('preserves an explicit id list', async () => {
            const { received } = await roundTrip({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: ENDPOINT,
                channelName: CHANNEL,
                fromUserId: 'user1',
                event: 'e',
                payload: {},
                requestId: 'r',
                recipientDescriptor: ['user2', 'user3'],
            });

            expect((received[0] as any).recipientDescriptor).toEqual(['user2', 'user3']);
        });
    });
});

describe('RedisDistributedBackend heartbeat', () => {
    it('delivers NODE_HEARTBEAT to peers on the heartbeat topic', async () => {
        jest.useFakeTimers();

        const nodeA = new RedisDistributedBackend({ namespace: 'hb-ns', heartbeatIntervalMs: 1_000 });
        const nodeB = new RedisDistributedBackend({ namespace: 'hb-ns', heartbeatIntervalMs: 1_000 });

        await nodeA.initialize();
        await nodeB.initialize();

        const seen: string[] = [];

        nodeB.subscribeToHeartbeats((nodeId) => seen.push(nodeId));

        jest.advanceTimersByTime(1_000);

        expect(seen).toContain(nodeA.nodeId);
        expect(seen).not.toContain(nodeB.nodeId);

        await nodeA.cleanup();
        await nodeB.cleanup();
        jest.useRealTimers();
    });
});
