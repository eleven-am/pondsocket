import { ServerActions, Subject, Unsubscribe, uuid } from '@eleven-am/pondsocket-common';
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
    return uuid();
}

type ChannelHandler = (message: DistributedChannelMessage) => void;

function isRecord (value: unknown): value is Record<string, any> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
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

    readonly #maxMessageSize: number;

    readonly #maxMessageAgeMs: number;

    readonly #maxFutureSkewMs: number;

    readonly #dedupeWindowSize: number;

    readonly #initializationTimeoutMs: number;

    readonly #cleanupTimeoutMs: number;

    readonly #channelHandlers: Map<string, Set<ChannelHandler>> = new Map();

    readonly #subscribedKeys: Set<string> = new Set();

    readonly #keyOperations: Map<string, Promise<void>> = new Map();

    readonly #seenMessageIds: Map<string, true> = new Map();

    #isConnected: boolean = false;

    #closed: boolean = false;

    #initializePromise: Promise<void> | null = null;

    #cleanupPromise: Promise<void> | null = null;

    #heartbeatTimer: ReturnType<typeof setInterval> | null = null;

    get nodeId (): string {
        return this.#nodeId;
    }

    get heartbeatTimeoutMs (): number {
        return this.#heartbeatTimeoutMs;
    }

    isHealthy (): boolean {
        return !this.#closed && this.#isConnected && this.#publishClient.isReady && this.#subscribeClient.isReady;
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
            maxMessageSize = 1024 * 1024,
            maxMessageAgeMs = 5 * 60_000,
            maxFutureSkewMs = 30_000,
            dedupeWindowSize = 4096,
            initializationTimeoutMs = 10_000,
            cleanupTimeoutMs = 5_000,
            onError = null,
        } = options;

        for (const [name, value] of Object.entries({
            heartbeatIntervalMs,
            heartbeatTimeoutMs,
            maxMessageSize,
            maxMessageAgeMs,
            maxFutureSkewMs,
            dedupeWindowSize,
            initializationTimeoutMs,
            cleanupTimeoutMs,
        })) {
            if (!Number.isSafeInteger(value) || value <= 0) {
                throw new TypeError(`${name} must be a positive safe integer`);
            }
        }
        if (heartbeatTimeoutMs < heartbeatIntervalMs) {
            throw new TypeError('heartbeatTimeoutMs must be greater than or equal to heartbeatIntervalMs');
        }

        this.#keyPrefix = keyPrefix;
        this.#namespace = namespace;
        this.#heartbeatIntervalMs = heartbeatIntervalMs;
        this.#heartbeatTimeoutMs = heartbeatTimeoutMs;
        this.#maxMessageSize = maxMessageSize;
        this.#maxMessageAgeMs = maxMessageAgeMs;
        this.#maxFutureSkewMs = maxFutureSkewMs;
        this.#dedupeWindowSize = dedupeWindowSize;
        this.#initializationTimeoutMs = initializationTimeoutMs;
        this.#cleanupTimeoutMs = cleanupTimeoutMs;
        this.#onError = onError;
        this.#nodeId = uuid();
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
        await this.initialize();
        this.#ensureOpen();
        const key = this.#buildKey(endpointName, channelName);
        const normalizedEndpointName = this.#normalizeTopicPart(endpointName);
        let handlers = this.#channelHandlers.get(key);

        if (!handlers) {
            handlers = new Set();
            this.#channelHandlers.set(key, handlers);
        }
        handlers.add(handler);

        try {
            await this.#enqueueKeyOperation(key, async () => {
                if (this.#subscribedKeys.has(key)) {
                    return;
                }

                await this.#subscribeClient.subscribe(key, (message) => {
                    this.#dispatchChannelMessage(key, normalizedEndpointName, channelName, message);
                });
                this.#subscribedKeys.add(key);
            });
        } catch (error) {
            handlers.delete(handler);
            if (handlers.size === 0) {
                this.#channelHandlers.delete(key);
            }
            throw error;
        }

        let active = true;

        return () => {
            if (!active) {
                return;
            }
            active = false;

            const current = this.#channelHandlers.get(key);

            current?.delete(handler);
            if (current?.size === 0) {
                this.#channelHandlers.delete(key);
            }

            void this.#enqueueKeyOperation(key, async () => {
                if (this.#channelHandlers.has(key) || !this.#subscribedKeys.has(key)) {
                    return;
                }
                await this.#subscribeClient.unsubscribe(key);
                this.#subscribedKeys.delete(key);
            }).catch((error) => this.#reportError(error, `Failed to unsubscribe ${key}`));
        };
    }

    subscribeToHeartbeats (handler: (nodeId: string) => void): Unsubscribe {
        return this.#heartbeatSubject.subscribe(handler);
    }

    async broadcast (endpointName: string, channelName: string, message: DistributedChannelMessage): Promise<void> {
        await this.initialize();
        this.#ensureOpen();
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

        if (Buffer.byteLength(serializedMessage) > this.#maxMessageSize) {
            throw new RangeError(`Distributed message exceeds the ${this.#maxMessageSize} byte limit`);
        }

        try {
            await this.#publishClient.publish(key, serializedMessage);
        } catch (error) {
            this.#reportError(error, `Failed to publish ${key}`);
            throw error;
        }
    }

    cleanup (): Promise<void> {
        if (this.#cleanupPromise) {
            return this.#cleanupPromise;
        }

        this.#closed = true;
        this.#cleanupPromise = this.#performCleanup();

        return this.#cleanupPromise;
    }

    async #performCleanup (): Promise<void> {
        if (this.#heartbeatTimer) {
            clearInterval(this.#heartbeatTimer);
            this.#heartbeatTimer = null;
        }

        this.#heartbeatSubject.close();
        this.#channelHandlers.clear();
        const pending = [
            ...this.#keyOperations.values(),
            ...(this.#initializePromise ? [this.#initializePromise] : []),
        ];

        // Terminal cleanup does not need Redis' QUIT handshake. Destroying the
        // sockets first rejects real node-redis commands immediately and also
        // interrupts an infinite reconnect loop.
        this.#destroyClients();

        if (pending.length > 0) {
            let timeout: ReturnType<typeof setTimeout> | undefined;
            const deadline = new Promise<void>((resolve) => {
                timeout = setTimeout(resolve, this.#cleanupTimeoutMs);
            });

            await Promise.race([Promise.allSettled(pending), deadline]);
            if (timeout) {
                clearTimeout(timeout);
            }
        }
        this.#keyOperations.clear();
        this.#subscribedKeys.clear();
        this.#seenMessageIds.clear();

        this.#isConnected = false;
    }

    initialize (): Promise<void> {
        this.#ensureOpen();
        if (this.#initializePromise) {
            return this.#initializePromise;
        }

        this.#initializePromise = this.#performInitialize();

        return this.#initializePromise;
    }

    async #performInitialize (): Promise<void> {
        let timeout: ReturnType<typeof setTimeout> | undefined;

        try {
            const initialize = (async () => {
                await Promise.all([
                    this.#publishClient.isOpen ? Promise.resolve() : this.#publishClient.connect(),
                    this.#subscribeClient.isOpen ? Promise.resolve() : this.#subscribeClient.connect(),
                ]);

                const heartbeatKey = this.#buildKey('__heartbeat__', '__heartbeat__');

                await this.#subscribeClient.subscribe(heartbeatKey, (message) => {
                    this.#handleHeartbeatMessage(message);
                });
            })();
            const timedOut = new Promise<never>((_resolve, reject) => {
                timeout = setTimeout(() => {
                    const error = new Error(
                        `Redis backend initialization timed out after ${this.#initializationTimeoutMs}ms`,
                    );

                    reject(error);
                    this.#destroyClients();
                }, this.#initializationTimeoutMs);
            });

            await Promise.race([initialize, timedOut]);

            if (this.#closed) {
                return;
            }
            this.#isConnected = true;
            this.#startHeartbeat();
        } catch (error) {
            this.#isConnected = false;
            this.#initializePromise = null;
            throw error;
        } finally {
            if (timeout) {
                clearTimeout(timeout);
            }
        }
    }

    #destroyClients (): void {
        for (const client of [this.#subscribeClient, this.#publishClient]) {
            try {
                client.destroy();
            } catch (_) {
                void 0;
            }
        }
    }

    #attachEventHandlers (client: RedisClientType, label: string): void {
        client.on('error', (err: Error) => {
            this.#isConnected = false;
            this.#reportError(err, `Redis ${label} client error`);
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
        this.#heartbeatTimer.unref?.();
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
            this.#reportError(err, 'Failed to publish heartbeat');
        });
    }

    #handleHeartbeatMessage (message: string): void {
        try {
            if (Buffer.byteLength(message) > this.#maxMessageSize) {
                return;
            }
            const parsedMessage = JSON.parse(message);

            if (this.#isValidMessage(parsedMessage, '__heartbeat__', '__heartbeat__') && parsedMessage.type === DistributedMessageType.NODE_HEARTBEAT && parsedMessage.sourceNodeId !== this.#nodeId && this.#markUnseen(parsedMessage)) {
                this.#heartbeatSubject.publish(parsedMessage.nodeId);
            }
        } catch (_) {
            void 0;
        }
    }

    #isValidMessage (message: any, endpointName?: string, channelName?: string): message is DistributedChannelMessage {
        if (!(
            isRecord(message) &&
            message.protocol === DISTRIBUTED_PROTOCOL &&
            message.version === DISTRIBUTED_VERSION &&
            typeof message.type === 'string' &&
            Object.values(DistributedMessageType).includes(message.type as DistributedMessageType) &&
            typeof message.messageId === 'string' &&
            typeof message.endpointName === 'string' &&
            typeof message.channelName === 'string' &&
            typeof message.sourceNodeId === 'string' &&
            Number.isFinite(message.timestamp)
        )) {
            return false;
        }

        if (endpointName !== undefined && message.endpointName !== endpointName) {
            return false;
        }
        if (channelName !== undefined && message.channelName !== channelName) {
            return false;
        }

        const now = Date.now();

        if (message.timestamp < now - this.#maxMessageAgeMs || message.timestamp > now + this.#maxFutureSkewMs) {
            return false;
        }

        const string = (value: unknown) => typeof value === 'string';
        const record = (value: unknown) => isRecord(value);
        const user = (value: unknown) => isRecord(value) && string(value.id) && record(value.assigns) && record(value.presence);

        switch (message.type) {
            case DistributedMessageType.STATE_REQUEST:
                return string(message.fromNode);
            case DistributedMessageType.STATE_RESPONSE:
                return Array.isArray(message.users) && message.users.every(user);
            case DistributedMessageType.USER_JOINED:
                return string(message.userId) && record(message.assigns) && record(message.presence);
            case DistributedMessageType.USER_LEFT:
            case DistributedMessageType.PRESENCE_REMOVED:
            case DistributedMessageType.USER_REMOVE:
                return string(message.userId);
            case DistributedMessageType.USER_MESSAGE:
                return string(message.fromUserId) && string(message.event) && string(message.requestId) && record(message.payload) &&
                    (message.action === undefined || (
                        Object.values(ServerActions).includes(message.action as ServerActions) &&
                        message.action !== ServerActions.PRESENCE
                    )) && (
                    string(message.recipientDescriptor) ||
                    (Array.isArray(message.recipientDescriptor) && message.recipientDescriptor.every(string))
                );
            case DistributedMessageType.PRESENCE_UPDATE:
                return string(message.userId) && record(message.presence);
            case DistributedMessageType.ASSIGNS_UPDATE:
                return string(message.userId) && (message.assigns === undefined || record(message.assigns));
            case DistributedMessageType.EVICT_USER:
                return string(message.userId) && string(message.reason);
            case DistributedMessageType.USER_GET_REQUEST:
                return string(message.userId) && string(message.requestId) && string(message.fromNode);
            case DistributedMessageType.USER_GET_RESPONSE:
                return string(message.userId) && string(message.requestId) && record(message.assigns) && record(message.presence);
            case DistributedMessageType.NODE_HEARTBEAT:
                return string(message.nodeId) && message.nodeId === message.sourceNodeId;
            default:
                return false;
        }
    }

    #buildKey (endpointName: string, channelName: string): string {
        const encode = (value: string) => encodeURIComponent(value);
        const prefix = `${encode(this.#keyPrefix)}:v1:${encode(this.#namespace)}`;

        if (endpointName === '__heartbeat__' && channelName === '__heartbeat__') {
            return `${prefix}:__heartbeat__`;
        }

        return `${prefix}:${encode(this.#normalizeTopicPart(endpointName))}:${encode(channelName)}`;
    }

    #normalizeTopicPart (value: string): string {
        return value.replace(/^\/+/, '');
    }

    #dispatchChannelMessage (key: string, endpointName: string, channelName: string, message: string): void {
        try {
            if (Buffer.byteLength(message) > this.#maxMessageSize) {
                return;
            }
            const parsedMessage = JSON.parse(message);

            if (!this.#isValidMessage(parsedMessage, endpointName, channelName) || parsedMessage.sourceNodeId === this.#nodeId || !this.#markUnseen(parsedMessage)) {
                return;
            }

            for (const handler of [...(this.#channelHandlers.get(key) ?? [])]) {
                try {
                    handler(parsedMessage);
                } catch (error) {
                    this.#reportError(error, `Distributed handler failed for ${key}`);
                }
            }
        } catch (_) {
            void 0;
        }
    }

    #markUnseen (message: DistributedChannelMessage): boolean {
        const id = message.messageId!;

        if (this.#seenMessageIds.has(id)) {
            return false;
        }
        this.#seenMessageIds.set(id, true);
        if (this.#seenMessageIds.size > this.#dedupeWindowSize) {
            const oldest = this.#seenMessageIds.keys().next().value as string | undefined;

            if (oldest !== undefined) {
                this.#seenMessageIds.delete(oldest);
            }
        }

        return true;
    }

    #enqueueKeyOperation (key: string, operation: () => Promise<void>): Promise<void> {
        const previous = this.#keyOperations.get(key) ?? Promise.resolve();
        const next = previous.catch(() => {}).then(operation);

        this.#keyOperations.set(key, next);
        void next.finally(() => {
            if (this.#keyOperations.get(key) === next) {
                this.#keyOperations.delete(key);
            }
        }).catch(() => {});

        return next;
    }

    #ensureOpen (): void {
        if (this.#closed) {
            throw new Error('Redis backend is closed');
        }
    }

    #reportError (error: unknown, context: string): void {
        if (!this.#onError) {
            return;
        }

        const cause = error instanceof Error ? error.message : String(error);

        try {
            this.#onError(new Error(`${context}: ${cause}`));
        } catch (_) {
            void 0;
        }
    }
}
