import { ChannelReceiver, ServerActions } from '@eleven-am/pondsocket-common';

import { RedisDistributedBackend } from '../abstracts/distributor';
import { DistributedChannelMessage, DistributedMessageType } from '../types';

const mockRedisControl = { failNextPublish: false,
    hangConnect: false,
    hangSubscribe: false,
    clients: [] as Array<{
        emit: (event: string, ...args: any[]) => void;
        setReady: (ready: boolean) => void;
    }> };

jest.mock('redis', () => {
    const channels = new Map<string, Set<(message: string) => void>>();

    const makeClient = () => {
        const own = new Map<string, (message: string) => void>();
        let isOpen = false;
        let isReady = false;
        let rejectConnect: ((error: Error) => void) | null = null;
        const eventHandlers = new Map<string, Array<(...args: any[]) => void>>();

        const client = {
            get isOpen () {
                return isOpen;
            },
            get isReady () {
                return isReady;
            },
            connect: async () => {
                isOpen = true;
                if (mockRedisControl.hangConnect) {
                    await new Promise<void>((_resolve, reject) => {
                        rejectConnect = reject;
                    });

                    return;
                }
                isReady = true;
            },
            duplicate: () => makeClient(),
            on: (event: string, handler: (...args: any[]) => void) => {
                const handlers = eventHandlers.get(event) ?? [];

                handlers.push(handler);
                eventHandlers.set(event, handlers);
            },
            emit: (event: string, ...args: any[]) => {
                eventHandlers.get(event)?.forEach((handler) => handler(...args));
            },
            setReady: (ready: boolean) => {
                isReady = ready;
            },
            subscribe: async (key: string, listener: (message: string) => void) => {
                if (mockRedisControl.hangSubscribe && !key.endsWith(':__heartbeat__')) {
                    await new Promise<void>(() => {});

                    return;
                }
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
                if (mockRedisControl.failNextPublish) {
                    mockRedisControl.failNextPublish = false;
                    throw new Error('publish unavailable');
                }
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
                isOpen = false;
                isReady = false;
            },
            destroy: () => {
                isOpen = false;
                isReady = false;
                rejectConnect?.(new Error('Redis client destroyed'));
                rejectConnect = null;
            },
        };

        mockRedisControl.clients.push(client);

        return client;
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
        mockRedisControl.failNextPublish = false;
        mockRedisControl.hangConnect = false;
        mockRedisControl.hangSubscribe = false;
        mockRedisControl.clients.length = 0;
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

    it('deduplicates messages and rejects messages for a different topic', async () => {
        const received: DistributedChannelMessage[] = [];
        const message: DistributedChannelMessage = {
            type: DistributedMessageType.USER_LEFT,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
            messageId: 'fixed-message-id',
            timestamp: Date.now(),
        };

        await nodeB.subscribeToChannel(ENDPOINT, CHANNEL, (value) => received.push(value));
        await nodeA.broadcast(ENDPOINT, CHANNEL, message);
        await nodeA.broadcast(ENDPOINT, CHANNEL, message);
        await nodeA.broadcast(ENDPOINT, CHANNEL, { ...message,
            messageId: 'wrong-topic-id',
            channelName: '/wrong-channel' });

        expect(received).toHaveLength(1);
        expect(received[0].messageId).toBe('fixed-message-id');
    });

    it('keeps a shared Redis subscription alive until the last local handler unsubscribes', async () => {
        const first = jest.fn();
        const second = jest.fn();
        const unsubscribeFirst = await nodeB.subscribeToChannel(ENDPOINT, CHANNEL, first);
        const unsubscribeSecond = await nodeB.subscribeToChannel(ENDPOINT, CHANNEL, second);

        unsubscribeFirst();
        await nodeA.broadcast(ENDPOINT, CHANNEL, {
            type: DistributedMessageType.USER_LEFT,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
        });

        expect(first).not.toHaveBeenCalled();
        expect(second).toHaveBeenCalledTimes(1);

        unsubscribeSecond();
    });

    it('keeps delimiter-containing endpoint and channel topics isolated', async () => {
        const first = jest.fn();
        const second = jest.fn();

        await nodeB.subscribeToChannel('/a:b', 'c', first);
        await nodeB.subscribeToChannel('/a', 'b:c', second);

        await nodeA.broadcast('/a:b', 'c', {
            type: DistributedMessageType.USER_LEFT,
            endpointName: '/a:b',
            channelName: 'c',
            userId: 'first-user',
        });
        await nodeA.broadcast('/a', 'b:c', {
            type: DistributedMessageType.USER_LEFT,
            endpointName: '/a',
            channelName: 'b:c',
            userId: 'second-user',
        });

        expect(first).toHaveBeenCalledTimes(1);
        expect((first.mock.calls[0][0] as any).userId).toBe('first-user');
        expect(second).toHaveBeenCalledTimes(1);
        expect((second.mock.calls[0][0] as any).userId).toBe('second-user');
    });

    it('reports Redis publish failures and rejects the broadcast', async () => {
        const onError = jest.fn();
        const node = new RedisDistributedBackend({ namespace: 'publish-error',
            onError });

        await node.initialize();
        mockRedisControl.failNextPublish = true;

        await expect(node.broadcast(ENDPOINT, CHANNEL, {
            type: DistributedMessageType.USER_LEFT,
            endpointName: ENDPOINT,
            channelName: CHANNEL,
            userId: 'user1',
        })).rejects.toThrow('publish unavailable');
        expect(onError).toHaveBeenCalledWith(expect.objectContaining({
            message: expect.stringContaining('Failed to publish'),
        }));

        await node.cleanup();
    });

    it('reports runtime disconnects, recovery, and terminal cleanup through health', async () => {
        const [publishClient, subscribeClient] = mockRedisControl.clients.slice(0, 2);

        expect(nodeA.isHealthy()).toBe(true);
        publishClient.setReady(false);
        publishClient.emit('error', new Error('connection lost'));
        expect(nodeA.isHealthy()).toBe(false);

        publishClient.setReady(true);
        subscribeClient.setReady(true);
        publishClient.emit('ready');
        expect(nodeA.isHealthy()).toBe(true);

        await nodeA.cleanup();
        expect(nodeA.isHealthy()).toBe(false);
    });

    it('bounds initialization when Redis remains unavailable', async () => {
        mockRedisControl.hangConnect = true;
        const node = new RedisDistributedBackend({
            namespace: 'initialization-timeout',
            initializationTimeoutMs: 20,
        });

        await expect(node.initialize()).rejects.toThrow('initialization timed out');
        mockRedisControl.hangConnect = false;
        await expect(node.initialize()).resolves.toBeUndefined();
        await expect(node.cleanup()).resolves.toBeUndefined();
    });

    it('cancels pending initialization promptly during cleanup', async () => {
        mockRedisControl.hangConnect = true;
        const node = new RedisDistributedBackend({
            namespace: 'initialization-cleanup',
            initializationTimeoutMs: 60_000,
        });
        const initialization = node.initialize();
        const cleanup = node.cleanup();
        const result = await Promise.race([
            cleanup.then(() => 'cleaned'),
            new Promise<string>((resolve) => setTimeout(() => resolve('timed-out'), 250)),
        ]);

        expect(result).toBe('cleaned');
        await expect(initialization).rejects.toThrow('destroyed');
    });

    it('bounds cleanup even when a Redis command never settles', async () => {
        const node = new RedisDistributedBackend({
            namespace: 'cleanup-timeout',
            cleanupTimeoutMs: 20,
        });

        await node.initialize();
        mockRedisControl.hangSubscribe = true;
        void node.subscribeToChannel(ENDPOINT, CHANNEL, jest.fn());
        await Promise.resolve();
        await Promise.resolve();

        const result = await Promise.race([
            node.cleanup().then(() => 'cleaned'),
            new Promise<string>((resolve) => setTimeout(() => resolve('timed-out'), 250)),
        ]);

        expect(result).toBe('cleaned');
    });

    it.each([
        ['initializationTimeoutMs', 0],
        ['cleanupTimeoutMs', Number.POSITIVE_INFINITY],
    ] as const)('rejects an invalid %s option', (option, value) => {
        expect(() => new RedisDistributedBackend({ [option]: value })).toThrow(TypeError);
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
            action: ServerActions.SYSTEM,
        });

        const msg = received[0] as any;

        expectEnvelope(msg, DistributedMessageType.USER_MESSAGE);
        expect(msg.fromUserId).toBe('user1');
        expect(msg.event).toBe('chat');
        expect(msg.payload).toEqual({ text: 'hi' });
        expect(msg.requestId).toBe('req-1');
        expect(msg.recipientDescriptor).toBe(ChannelReceiver.ALL_USERS);
        expect(msg.action).toBe(ServerActions.SYSTEM);
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
