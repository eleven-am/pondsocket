import { Subject, Unsubscribe } from '@eleven-am/pondsocket-common';
import { createClient, RedisClientType } from 'redis';

import { DistributedChannelMessage, IDistributedBackend } from '../types';
import { DistributedMessageType } from './types';

interface RedisDistributedBackendOptions {
    host?: string;
    port?: number;
    password?: string;
    database?: number;
    url?: string;
    keyPrefix?: string;
}

export class RedisDistributedBackend implements IDistributedBackend {
    readonly #publishClient: RedisClientType;

    readonly #subscribeClient: RedisClientType;

    readonly #keyPrefix: string;

    readonly #subject: Subject<DistributedChannelMessage>;

    readonly #nodeId: string;

    #isConnected: boolean = false;

    constructor (options: RedisDistributedBackendOptions = {}) {
        const {
            host = 'localhost',
            port = 6379,
            password,
            database = 0,
            url,
            keyPrefix = 'pondsocket',
        } = options;

        this.#keyPrefix = keyPrefix;
        this.#nodeId = Math.random().toString(36)
            .substring(2, 15);
        this.#subject = new Subject<DistributedChannelMessage & { nodeId: string }>();

        // Create Redis clients
        const clientConfig = url
            ? { url }
            : { socket: { host,
                port },
            password,
            database };

        this.#publishClient = createClient(clientConfig);
        this.#subscribeClient = this.#publishClient.duplicate();

        void this.#initialize();
    }

    /**
     * Gets the subject for subscribing to distributed messages
     */
    get subject (): Subject<DistributedChannelMessage & { nodeId: string }> {
        return this.#subject as Subject<DistributedChannelMessage & { nodeId: string }>;
    }

    /**
     * Subscribe to messages for a specific endpoint and channel
     */
    subscribe (endpointName: string, channelName: string, handler: (message: DistributedChannelMessage) => void): Unsubscribe {
        return this.subject.subscribe((message) => {
            if (message.endpointName === endpointName && message.channelName === channelName && message.nodeId !== this.#nodeId) {
                handler(message);
            }
        });
    }

    /**
     * Broadcasts a message to all nodes for a specific endpoint and channel
     */
    async broadcast (endpointName: string, channelName: string, message: DistributedChannelMessage): Promise<void> {
        if (!this.#isConnected) {
            throw new Error('Redis backend is not connected');
        }

        const key = this.#buildKey(endpointName, channelName);
        const distributedMessage = {
            ...message,
            nodeId: this.#nodeId,
        };

        const serializedMessage = JSON.stringify(distributedMessage);

        await this.#publishClient.publish(key, serializedMessage);
    }

    /**
     * Cleanup and close all Redis connections
     */
    async cleanup (): Promise<void> {
        this.subject.close();

        if (this.#subscribeClient.isOpen) {
            await this.#subscribeClient.quit();
        }

        if (this.#publishClient.isOpen) {
            await this.#publishClient.quit();
        }

        this.#isConnected = false;
    }

    /**
     * Initialize Redis connections and setup subscription
     */
    async #initialize (): Promise<void> {
        await Promise.all([
            this.#publishClient.connect(),
            this.#subscribeClient.connect(),
        ]);

        const pattern = `${this.#keyPrefix}:*`;

        await this.#subscribeClient.pSubscribe(pattern, (message) => {
            this.#handleRedisMessage(message);
        });

        this.#isConnected = true;
    }

    /**
     * Handle incoming Redis messages and publish to subject
     */
    #handleRedisMessage (message: string): void {
        try {
            const parsedMessage: DistributedChannelMessage & { nodeId: string } = JSON.parse(message);

            if (this.#isValidMessage(parsedMessage)) {
                this.subject.publish(parsedMessage);
            }
        } catch {
            // Silently ignore invalid messages
        }
    }

    /**
     * Validate that a message has the required structure
     */
    #isValidMessage (message: any): message is DistributedChannelMessage {
        return (
            typeof message === 'object' &&
            typeof message.type === 'string' &&
            Object.values(DistributedMessageType).includes(message.type) &&
            typeof message.endpointName === 'string' &&
            typeof message.channelName === 'string'
        );
    }

    /**
     * Build the Redis key for endpoint and channel
     */
    #buildKey (endpointName: string, channelName: string): string {
        return `${this.#keyPrefix}:${endpointName}:${channelName}`;
    }
}

