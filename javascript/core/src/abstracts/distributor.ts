import { Subject, Unsubscribe } from '@eleven-am/pondsocket-common';
import { createClient, RedisClientType } from 'redis';

import {
    DistributedChannelMessage,
    DistributedMessageType,
    IDistributedBackend,
    RedisDistributedBackendOptions,
} from '../types';

const DISTRIBUTED_PROTOCOL = 'pondsocket.distributed';
const DISTRIBUTED_VERSION = 1;

function distributedId (): string {
    return `${Date.now().toString(36)}-${Math.random().toString(36)
        .slice(2)}`;
}

export class RedisDistributedBackend implements IDistributedBackend {
    readonly #publishClient: RedisClientType;

    readonly #subscribeClient: RedisClientType;

    readonly #keyPrefix: string;

    readonly #namespace: string;

    readonly #heartbeatSubject: Subject<string>;

    readonly #nodeId: string;

    readonly #heartbeatIntervalMs: number;

    readonly #heartbeatTimeoutMs: number;

    readonly #onError: ((error: Error) => void) | null;

    #isConnected: boolean = false;

    #heartbeatTimer: ReturnType<typeof setInterval> | null = null;

    get nodeId (): string {
        return this.#nodeId;
    }

    get heartbeatTimeoutMs (): number {
        return this.#heartbeatTimeoutMs;
    }

    constructor (options: RedisDistributedBackendOptions = {}) {
        const {
            host = 'localhost',
            port = 6379,
            password,
            database = 0,
            url,
            keyPrefix = 'pondsocket',
            namespace = 'default',
            heartbeatIntervalMs = 30_000,
            heartbeatTimeoutMs = 90_000,
            onError = null,
        } = options;

        this.#keyPrefix = keyPrefix;
        this.#namespace = namespace;
        this.#heartbeatIntervalMs = heartbeatIntervalMs;
        this.#heartbeatTimeoutMs = heartbeatTimeoutMs;
        this.#onError = onError;
        this.#nodeId = Math.random().toString(36)
            .substring(2, 15);
        this.#heartbeatSubject = new Subject<string>();

        const reconnectStrategy = (retries: number) => {
            const delay = Math.min(retries * 100, 5000);

            return delay;
        };

        const clientConfig = url
            ? { url,
                socket: { reconnectStrategy } }
            : {
                socket: { host,
                    port,
                    reconnectStrategy },
                password,
                database,
            };

        this.#publishClient = createClient(clientConfig);
        this.#subscribeClient = this.#publishClient.duplicate();

        this.#attachEventHandlers(this.#publishClient, 'publish');
        this.#attachEventHandlers(this.#subscribeClient, 'subscribe');
    }

    async subscribeToChannel (endpointName: string, channelName: string, handler: (message: DistributedChannelMessage) => void): Promise<Unsubscribe> {
        const key = this.#buildKey(endpointName, channelName);

        const listener = (message: string) => {
            try {
                const parsedMessage: DistributedChannelMessage & { nodeId?: string } = JSON.parse(message);

                if (!this.#isValidMessage(parsedMessage)) {
                    return;
                }

                if (parsedMessage.sourceNodeId !== this.#nodeId) {
                    handler(parsedMessage);
                }
            } catch (_) {
                void 0;
            }
        };

        await this.#subscribeClient.subscribe(key, listener);

        return () => {
            this.#subscribeClient.unsubscribe(key).catch(() => {});
        };
    }

    subscribeToHeartbeats (handler: (nodeId: string) => void): Unsubscribe {
        return this.#heartbeatSubject.subscribe(handler);
    }

    async broadcast (endpointName: string, channelName: string, message: DistributedChannelMessage): Promise<void> {
        if (!this.#isConnected) {
            throw new Error('Redis backend is not connected');
        }

        const key = this.#buildKey(endpointName, channelName);
        const normalizedEndpointName = this.#normalizeTopicPart(endpointName);
        const distributedMessage = {
            ...message,
            protocol: DISTRIBUTED_PROTOCOL,
            version: DISTRIBUTED_VERSION,
            messageId: message.messageId ?? distributedId(),
            timestamp: message.timestamp ?? Date.now(),
            sourceNodeId: this.#nodeId,
            endpointName: normalizedEndpointName,
        };

        const serializedMessage = JSON.stringify(distributedMessage);

        await this.#publishClient.publish(key, serializedMessage);
    }

    async cleanup (): Promise<void> {
        if (this.#heartbeatTimer) {
            clearInterval(this.#heartbeatTimer);
            this.#heartbeatTimer = null;
        }

        this.#heartbeatSubject.close();

        if (this.#subscribeClient.isOpen) {
            await this.#subscribeClient.quit();
        }

        if (this.#publishClient.isOpen) {
            await this.#publishClient.quit();
        }

        this.#isConnected = false;
    }

    async initialize (): Promise<void> {
        await Promise.all([
            this.#publishClient.connect(),
            this.#subscribeClient.connect(),
        ]);

        const heartbeatKey = this.#buildKey('__heartbeat__', '__heartbeat__');

        await this.#subscribeClient.subscribe(heartbeatKey, (message) => {
            this.#handleHeartbeatMessage(message);
        });

        this.#isConnected = true;
        this.#startHeartbeat();
    }

    #attachEventHandlers (client: RedisClientType, label: string): void {
        client.on('error', (err: Error) => {
            this.#isConnected = false;
            if (this.#onError) {
                this.#onError(new Error(`Redis ${label} client error: ${err.message}`));
            }
        });

        client.on('ready', () => {
            if (this.#publishClient.isReady && this.#subscribeClient.isReady) {
                this.#isConnected = true;
            }
        });

        client.on('end', () => {
            this.#isConnected = false;
        });
    }

    #startHeartbeat (): void {
        this.#publishHeartbeat();

        this.#heartbeatTimer = setInterval(() => {
            this.#publishHeartbeat();
        }, this.#heartbeatIntervalMs);
    }

    #publishHeartbeat (): void {
        if (!this.#isConnected) {
            return;
        }

        const heartbeatMessage = {
            protocol: DISTRIBUTED_PROTOCOL,
            version: DISTRIBUTED_VERSION,
            type: DistributedMessageType.NODE_HEARTBEAT,
            messageId: distributedId(),
            endpointName: '__heartbeat__',
            channelName: '__heartbeat__',
            sourceNodeId: this.#nodeId,
            timestamp: Date.now(),
            nodeId: this.#nodeId,
        };

        const key = this.#buildKey('__heartbeat__', '__heartbeat__');

        this.#publishClient.publish(key, JSON.stringify(heartbeatMessage)).catch((err: Error) => {
            if (this.#onError) {
                this.#onError(new Error(`Failed to publish heartbeat: ${err.message}`));
            }
        });
    }

    #handleHeartbeatMessage (message: string): void {
        try {
            const parsedMessage = JSON.parse(message);

            if (this.#isValidMessage(parsedMessage) && parsedMessage.type === DistributedMessageType.NODE_HEARTBEAT && parsedMessage.sourceNodeId && parsedMessage.sourceNodeId !== this.#nodeId) {
                this.#heartbeatSubject.publish(parsedMessage.sourceNodeId);
            }
        } catch (_) {
            void 0;
        }
    }

    #isValidMessage (message: any): message is DistributedChannelMessage {
        return (
            typeof message === 'object' &&
            message.protocol === DISTRIBUTED_PROTOCOL &&
            message.version === DISTRIBUTED_VERSION &&
            typeof message.type === 'string' &&
            Object.values(DistributedMessageType).includes(message.type) &&
            typeof message.messageId === 'string' &&
            typeof message.endpointName === 'string' &&
            typeof message.channelName === 'string' &&
            typeof message.sourceNodeId === 'string' &&
            typeof message.timestamp === 'number'
        );
    }

    #buildKey (endpointName: string, channelName: string): string {
        if (endpointName === '__heartbeat__' && channelName === '__heartbeat__') {
            return `${this.#keyPrefix}:v1:${this.#namespace}:__heartbeat__`;
        }

        return `${this.#keyPrefix}:v1:${this.#namespace}:${this.#normalizeTopicPart(endpointName)}:${channelName}`;
    }

    #normalizeTopicPart (value: string): string {
        return value.replace(/^\/+/, '');
    }
}
