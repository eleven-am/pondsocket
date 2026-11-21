import { createClient } from 'redis';

import { RedisDistributedBackend } from '../abstracts/distributor';
import { DistributedMessageType } from '../types';

const redisUrl = process.env.PONDSOCKET_REDIS_URL;
const describeRedis = redisUrl ? describe : describe.skip;

const waitFor = async (predicate: () => boolean, timeoutMs = 2_000): Promise<void> => {
    const deadline = Date.now() + timeoutMs;

    while (!predicate()) {
        if (Date.now() >= deadline) {
            throw new Error('Timed out waiting for Redis delivery');
        }
        await new Promise((resolve) => setTimeout(resolve, 10));
    }
};

describeRedis('RedisDistributedBackend live integration', () => {
    const namespace = `integration-${process.pid}-${Date.now()}`;
    let nodeA: RedisDistributedBackend;
    let nodeB: RedisDistributedBackend;

    beforeEach(async () => {
        nodeA = new RedisDistributedBackend({ url: redisUrl!,
            namespace,
            maxMessageSize: 512 });
        nodeB = new RedisDistributedBackend({ url: redisUrl!,
            namespace,
            maxMessageSize: 512 });
        await Promise.all([nodeA.initialize(), nodeB.initialize()]);
    });

    afterEach(async () => {
        await Promise.all([nodeA.cleanup(), nodeB.cleanup()]);
    });

    it('delivers between nodes and reference-counts local handlers', async () => {
        const first = jest.fn();
        const second = jest.fn();
        const unsubscribeFirst = await nodeB.subscribeToChannel('/live', '/room:1', first);
        const unsubscribeSecond = await nodeB.subscribeToChannel('/live', '/room:1', second);

        await nodeA.broadcast('/live', '/room:1', {
            type: DistributedMessageType.USER_LEFT,
            endpointName: '/live',
            channelName: '/room:1',
            userId: 'first-user',
        });
        await waitFor(() => first.mock.calls.length === 1 && second.mock.calls.length === 1);

        unsubscribeFirst();
        await nodeA.broadcast('/live', '/room:1', {
            type: DistributedMessageType.USER_LEFT,
            endpointName: '/live',
            channelName: '/room:1',
            userId: 'second-user',
        });
        await waitFor(() => second.mock.calls.length === 2);

        expect(first).toHaveBeenCalledTimes(1);
        expect(second).toHaveBeenCalledTimes(2);

        unsubscribeSecond();
    });

    it('filters malformed and oversized PubSub payloads and cleans up terminally', async () => {
        const handler = jest.fn();
        const rawClient = createClient({ url: redisUrl! });
        const topic = `${encodeURIComponent('pondsocket')}:v1:${encodeURIComponent(namespace)}:${encodeURIComponent('live')}:${encodeURIComponent('/room:1')}`;

        await nodeB.subscribeToChannel('/live', '/room:1', handler);
        await rawClient.connect();
        await rawClient.publish(topic, '{not-json');
        await rawClient.publish(topic, 'x'.repeat(513));
        await new Promise((resolve) => setTimeout(resolve, 100));

        expect(handler).not.toHaveBeenCalled();

        await rawClient.quit();
        await nodeB.cleanup();
        await expect(nodeB.broadcast('/live', '/room:1', {
            type: DistributedMessageType.USER_LEFT,
            endpointName: '/live',
            channelName: '/room:1',
            userId: 'user1',
        })).rejects.toThrow('closed');
    });

    it('restores channel delivery after Redis kills PubSub connections', async () => {
        const handler = jest.fn();
        const admin = createClient({ url: redisUrl! });

        await nodeB.subscribeToChannel('/live', '/reconnect', handler);
        await admin.connect();
        await admin.sendCommand(['CLIENT', 'KILL', 'TYPE', 'PUBSUB']);
        await admin.quit();

        const deadline = Date.now() + 5_000;

        while (handler.mock.calls.length === 0 && Date.now() < deadline) {
            try {
                await nodeA.broadcast('/live', '/reconnect', {
                    type: DistributedMessageType.USER_LEFT,
                    endpointName: '/live',
                    channelName: '/reconnect',
                    userId: 'reconnected-user',
                });
            } catch (_) {
                void 0;
            }
            await new Promise((resolve) => setTimeout(resolve, 50));
        }

        expect(handler).toHaveBeenCalled();
        expect((handler.mock.calls[0][0] as any).userId).toBe('reconnected-user');
    }, 10_000);
});
