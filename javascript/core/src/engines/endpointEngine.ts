import {
    ChannelEvent,
    AnyPondChannelDefinition,
    AnyPondSchema,
    compilePondRoute,
    ClientActions,
    ClientMessage,
    clientMessageSchema,
    ErrorTypes,
    Events,
    JoinParams,
    isPondChannelDefinition,
    PathOf,
    PondPath,
    SchemaOf,
    ServerActions,
    SystemSender,
    uuid,
} from '@eleven-am/pondsocket-common';
import { RawData, WebSocket } from 'ws';

import { LobbyEngine } from './lobbyEngine';
import { Middleware } from '../abstracts/middleware';
import { AuthorizationHandler, JoinRequestOptions, RequestCache, SocketCache } from '../abstracts/types';
import { JoinContext } from '../contexts/joinContext';
import { HttpError } from '../errors/httpError';
import { parseAddress } from '../matcher/matcher';
import { IDistributedBackend } from '../types';
import { PondChannel } from '../wrappers/pondChannel';

interface PendingInboundMessage {
    message: ClientMessage;
    bytes: number;
}

interface InboundQueue {
    items: PendingInboundMessage[];
    bytes: number;
    running: boolean;
    activeBytes: number;
    terminal: boolean;
}

export class EndpointEngine {
    readonly #sockets: Map<string, SocketCache>;

    readonly #backend: IDistributedBackend | null;

    readonly #middleware: Middleware<RequestCache, JoinParams>;

    readonly #lobbyEngines: Map<PondPath<string>, LobbyEngine>;

    readonly #maxMessageSize: number;

    readonly #maxBufferedAmount: number;

    readonly #joinTimeout: number;

    readonly #maxPendingJoins: number;

    readonly #pendingJoinCounts: Map<SocketCache, number> = new Map();

    readonly #inboundQueues: Map<SocketCache, InboundQueue> = new Map();

    readonly #maxPendingMessagesPerSocket: number;

    readonly #maxPendingBytesPerSocket: number;

    readonly #eventTimeout: number;

    readonly #maxPendingMessagesPerUser: number;

    readonly #maxPendingBytesPerUser: number;

    readonly #onError: ((error: Error) => void) | null;

    #closed: boolean = false;

    constructor (public readonly path: string, backend: IDistributedBackend | null, maxMessageSize: number = 1024 * 1024, maxBufferedAmount: number = maxMessageSize * 4, onError: ((error: Error) => void) | null = null, joinTimeout: number = 10_000, maxPendingJoins: number = 32, maxPendingMessagesPerUser: number = 256, maxPendingBytesPerUser: number = maxBufferedAmount, maxPendingMessagesPerSocket: number = 64, maxPendingBytesPerSocket: number = maxMessageSize * 4, eventTimeout: number = 10_000) {
        this.#sockets = new Map();
        this.#lobbyEngines = new Map();
        this.#middleware = new Middleware();
        this.#backend = backend || null;
        this.#maxMessageSize = maxMessageSize;
        this.#maxBufferedAmount = maxBufferedAmount;
        this.#onError = onError;
        this.#joinTimeout = joinTimeout;
        this.#maxPendingJoins = maxPendingJoins;
        this.#maxPendingMessagesPerUser = maxPendingMessagesPerUser;
        this.#maxPendingBytesPerUser = maxPendingBytesPerUser;
        this.#maxPendingMessagesPerSocket = maxPendingMessagesPerSocket;
        this.#maxPendingBytesPerSocket = maxPendingBytesPerSocket;
        this.#eventTimeout = eventTimeout;
    }

    /**
     * Creates a new channel on a specified path
     */
    createChannel<Definition extends AnyPondChannelDefinition> (definition: Definition, handler: AuthorizationHandler<PathOf<Definition>, SchemaOf<Definition>>): PondChannel<SchemaOf<Definition>>;

    createChannel<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> (path: PondPath<Path>, handler: AuthorizationHandler<Path, Schema>): PondChannel<Schema>;

    createChannel<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> (pathOrDefinition: PondPath<Path> | AnyPondChannelDefinition, handler: AuthorizationHandler<Path, Schema>) {
        this.#ensureOpen();
        const path = (isPondChannelDefinition(pathOrDefinition) ? pathOrDefinition.path : pathOrDefinition) as PondPath<Path>;

        if (typeof path === 'string') {
            compilePondRoute(path);
        }

        if (this.#lobbyEngines.has(path)) {
            throw new HttpError(409, `GatewayEngine: Channel pattern ${String(path)} is already registered`);
        }

        const lobbyEngine = new LobbyEngine(this, this.#backend, this.#maxPendingMessagesPerUser, this.#maxPendingBytesPerUser, this.#eventTimeout);

        this.#middleware.use((user, joinParams, next) => {
            const event = parseAddress<Path>(path, user.channelName);

            if (event) {
                const options: JoinRequestOptions<Path, Schema> = {
                    clientId: user.clientId,
                    assigns: user.assigns,
                    params: event,
                    joinParams,
                };

                const channel = lobbyEngine.getOrCreateChannel(user.channelName);
                const context = new JoinContext<Path, Schema>(options, channel, user);

                let delegated = false;
                let finishAuthorization = () => {};
                let cancelled = false;
                const delegate = (error?: HttpError) => {
                    delegated = true;
                    const result = next(error);

                    finishAuthorization();

                    return result;
                };
                const authorization = new Promise<void>((resolve, reject) => {
                    let settled = false;
                    const settle = (error?: unknown) => {
                        if (settled) {
                            return;
                        }
                        settled = true;
                        clearTimeout(timer);
                        user.pendingJoinAuthorizations?.delete(cancel);
                        if (error !== undefined) {
                            reject(error);
                        } else {
                            resolve();
                        }
                    };
                    const cancel = () => {
                        cancelled = true;
                        context.cancel();
                        settle();
                    };
                    const timer = setTimeout(() => {
                        if (!delegated && !context.hasResponded) {
                            context.decline('Join authorization timed out', 408);
                        }
                        settle();
                    }, this.#joinTimeout);

                    timer.unref?.();
                    finishAuthorization = settle;
                    user.pendingJoinAuthorizations?.add(cancel);
                    void Promise.resolve().then(() => handler(context, delegate)).then(
                        () => settle(),
                        (error) => settle(error),
                    );
                });

                return authorization.then(() => {
                    if (!cancelled && !delegated && !context.hasResponded) {
                        context.decline('Join handler completed without accepting or declining the request', 500);
                    }
                }).finally(() => {
                    if (!delegated) {
                        user.completeJoinAuthorization?.();
                    }
                    channel.closeIfUnused();
                });
            }

            next();
        });

        this.#lobbyEngines.set(path, lobbyEngine);

        return new PondChannel<Schema>(lobbyEngine);
    }

    /**
     * Gets all connected clients
     */
    getClients () {
        return [...this.#sockets.values()];
    }

    /**
     * Gets a specific user by client ID
     */
    getUser (clientId: string) {
        const user = this.#sockets.get(clientId);

        if (!user) {
            throw new HttpError(404, `GatewayEngine: User ${clientId} does not exist`);
        }

        return user;
    }

    /**
     * Manages a new WebSocket connection
     */
    manageSocket (cache: SocketCache) {
        if (this.#closed) {
            cache.socket.close(1012, 'Endpoint is closed');

            return;
        }

        const existing = this.#sockets.get(cache.clientId);

        if (existing && existing !== cache) {
            existing.socket.close(1008, 'Connection id replaced');
            this.#cleanupSocket(existing);
        }

        this.#sockets.set(cache.clientId, cache);
        cache.socket.on('message', (message: RawData, isBinary: boolean) => this.#readMessage(cache, message, isBinary));
        cache.socket.on('close', () => this.#handleSocketClose(cache));
        cache.socket.on('error', () => {});

        const event: ChannelEvent = {
            event: Events.CONNECTION,
            action: ServerActions.CONNECT,
            channelName: SystemSender.ENDPOINT,
            requestId: uuid(),
            payload: {},
        };

        this.sendMessage(cache.socket, event);
    }

    /**
     * Sends a message to a WebSocket
     */
    sendMessage (socket: WebSocket, message: ChannelEvent) {
        if (socket.readyState !== WebSocket.OPEN) {
            return;
        }

        let serialized: string;

        try {
            serialized = JSON.stringify(message);
        } catch (error) {
            this.reportError(error, 'Failed to serialize an outbound message');
            socket.terminate();

            return;
        }

        const messageBytes = Buffer.byteLength(serialized);

        if (messageBytes > this.#maxMessageSize || socket.bufferedAmount + messageBytes > this.#maxBufferedAmount) {
            socket.close(1009, 'Outbound buffer limit exceeded');

            return;
        }

        try {
            socket.send(serialized, (error) => {
                if (error && socket.readyState !== WebSocket.CLOSED) {
                    this.reportError(error, 'WebSocket send failed');
                    socket.terminate();
                }
            });
        } catch (error) {
            this.reportError(error, 'WebSocket send failed');
            socket.terminate();
        }
    }

    reportError (error: unknown, context: string): void {
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

    close (): void {
        if (this.#closed) {
            return;
        }

        this.#closed = true;
        const sockets = [...this.#sockets.values()];
        const lobbies = [...this.#lobbyEngines.values()];

        this.#sockets.clear();
        this.#pendingJoinCounts.clear();
        this.#inboundQueues.clear();
        this.#lobbyEngines.clear();
        sockets.forEach((cache) => {
            this.#cleanupSocket(cache);
            if (cache.socket.readyState !== WebSocket.CLOSED) {
                cache.socket.close(1001, 'Endpoint shutting down');
            }
        });
        lobbies.forEach((lobby) => lobby.close());
    }

    /**
     * Closes one or more client connections
     */
    closeConnection (clientIds: string | string[]) {
        const clients = typeof clientIds === 'string' ? [clientIds] : clientIds;

        this.getClients()
            .forEach(({ clientId, socket }) => {
                if (clients.includes(clientId)) {
                    socket.close();
                }
            });
    }

    /**
     * Handles a socket closing
     */
    #handleSocketClose (cache: SocketCache) {
        if (this.#sockets.get(cache.clientId) !== cache) {
            return;
        }

        try {
            this.#sockets.delete(cache.clientId);
            this.#cleanupSocket(cache);
        } catch {
            void 0;
        }
    }

    #cleanupSocket (cache: SocketCache): void {
        cache.subscriptions.forEach((unsubscribe) => {
            try {
                unsubscribe();
            } catch (_) {
                void 0;
            }
        });
        cache.subscriptions.clear();
        cache.channelSubscriptions?.clear();
        cache.pendingJoinAuthorizations?.forEach((cancel) => {
            try {
                cancel();
            } catch (_) {
                void 0;
            }
        });
        cache.pendingJoinAuthorizations?.clear();
        this.#pendingJoinCounts.delete(cache);
        const inboundQueue = this.#inboundQueues.get(cache);

        if (inboundQueue) {
            inboundQueue.terminal = true;
            inboundQueue.items.length = 0;
            inboundQueue.bytes = inboundQueue.activeBytes;
            if (!inboundQueue.running) {
                this.#inboundQueues.delete(cache);
            }
        }
    }

    /**
     * Processes incoming messages from clients
     */
    async #handleMessage (cache: SocketCache, message: ClientMessage): Promise<void> {
        switch (message.action) {
            case ClientActions.JOIN_CHANNEL:
                await this.#joinChannel(message.channelName, cache, message.payload, message.requestId);
                break;

            case ClientActions.LEAVE_CHANNEL:
                this.#leaveChannel(message.channelName, cache);
                break;

            case ClientActions.BROADCAST:
                await this.#broadcastMessage(cache, message);
                break;

            default:
                throw new Error(`GatewayEngine: Action ${message.action} does not exist`);
        }
    }

    /**
     * Handles a join channel request
     */
    #joinChannel (channel: string, socket: SocketCache, joinParams: JoinParams, requestId: string): Promise<void> {
        const pending = this.#pendingJoinCounts.get(socket) ?? 0;

        if (pending >= this.#maxPendingJoins) {
            this.sendMessage(socket.socket, this.#buildError(
                new HttpError(429, 'Too many pending channel joins'),
                ErrorTypes.CHANNEL_ERROR,
                channel,
                requestId,
            ));

            return Promise.resolve();
        }

        this.#pendingJoinCounts.set(socket, pending + 1);
        socket.pendingJoinAuthorizations ??= new Set();
        let completed = false;
        let resolveAuthorization!: () => void;
        const authorization = new Promise<void>((resolve) => resolveAuthorization = resolve);
        const completeJoinAuthorization = () => {
            if (completed) {
                return;
            }
            completed = true;
            const current = this.#pendingJoinCounts.get(socket) ?? 0;

            if (current <= 1) {
                this.#pendingJoinCounts.delete(socket);
            } else {
                this.#pendingJoinCounts.set(socket, current - 1);
            }
            resolveAuthorization();
        };
        const cache: RequestCache = {
            ...socket,
            requestId,
            channelName: channel,
            completeJoinAuthorization,
        };

        this.#middleware.run(cache, joinParams, (error) => {
            completeJoinAuthorization();
            const requestError = error || new HttpError(404, `GatewayEngine: Channel ${channel} does not exist`);
            const errorType = error ? ErrorTypes.CHANNEL_ERROR : ErrorTypes.CHANNEL_NOT_FOUND;

            this.sendMessage(socket.socket, this.#buildError(requestError, errorType, channel, requestId));
        });

        return authorization;
    }

    /**
     * Handles a leave channel request
     */
    #leaveChannel (channel: string, socket: SocketCache) {
        const subscription = socket.channelSubscriptions?.get(channel);

        if (subscription) {
            subscription();

            return;
        }

        try {
            const engine = this.#retrieveChannel(channel);

            engine.removeUser(socket.clientId);
        } catch (error) {
            if (!(error instanceof HttpError) || error.statusCode !== 404) {
                throw error;
            }
        }
    }

    /**
     * Gets a channel engine for a given channel name
     */
    #retrieveChannel (channel: string) {
        for (const [path, lobby] of this.#lobbyEngines) {
            const event = parseAddress(path, channel);

            if (event) {
                return lobby.getChannel(channel);
            }
        }

        throw new HttpError(404, `GatewayEngine: Channel ${channel} does not exist`);
    }

    /**
     * Builds an error response
     */
    #buildError (
        error: unknown,
        errorType: ErrorTypes = ErrorTypes.INVALID_MESSAGE,
        channelName: string = SystemSender.ENDPOINT,
        requestId: string = uuid(),
    ) {
        let message: string;
        let status: number;

        if (error instanceof HttpError) {
            message = error.message;
            status = error.statusCode;
        } else if (error instanceof Error) {
            message = error.message;
            status = 500;
        } else {
            message = 'An unknown error occurred';
            status = 500;
        }

        if (status >= 500) {
            this.reportError(error, 'Internal endpoint error');
            message = 'Internal server error';
        }

        const event: ChannelEvent = {
            event: errorType,
            action: ServerActions.ERROR,
            channelName,
            requestId,
            payload: {
                code: errorType,
                message,
                status,
                statusCode: status,
                error: {
                    message,
                    status,
                },
            },
        };

        return event;
    }

    /**
     * Broadcasts a message from a client
     */
    async #broadcastMessage (socket: SocketCache, message: ClientMessage): Promise<void> {
        const engine = this.#retrieveChannel(message.channelName);

        await engine.broadcastMessage(socket.clientId, message);
    }

    /**
     * Reads and processes a message from a WebSocket
     */
    #readMessage (cache: SocketCache, message: RawData, isBinary: boolean) {
        try {
            if (this.#closed || this.#sockets.get(cache.clientId) !== cache) {
                return;
            }

            if (isBinary) {
                throw new HttpError(415, 'Binary WebSocket messages are not supported');
            }

            const bytes = Array.isArray(message)
                ? Buffer.concat(message)
                : Buffer.isBuffer(message)
                    ? message
                    : Buffer.from(message);

            if (bytes.byteLength > this.#maxMessageSize) {
                throw new HttpError(413, `Message size ${bytes.byteLength} exceeds maximum allowed size of ${this.#maxMessageSize} bytes`);
            }

            const data = JSON.parse(bytes.toString('utf8'));
            const result = clientMessageSchema.parse(data);

            this.#enqueueInbound(cache, result, bytes.byteLength);
        } catch (e) {
            this.sendMessage(cache.socket, this.#buildError(e));
        }
    }

    #enqueueInbound (cache: SocketCache, message: ClientMessage, bytes: number): void {
        let queue = this.#inboundQueues.get(cache);

        if (!queue) {
            queue = { items: [],
                bytes: 0,
                running: false,
                activeBytes: 0,
                terminal: false };
            this.#inboundQueues.set(cache, queue);
        }

        if (queue.terminal || cache.socket.readyState !== WebSocket.OPEN) {
            return;
        }

        const pendingCount = queue.items.length + (queue.running ? 1 : 0);

        if (pendingCount >= this.#maxPendingMessagesPerSocket || queue.bytes + bytes > this.#maxPendingBytesPerSocket) {
            queue.terminal = true;
            queue.items.length = 0;
            queue.bytes = queue.activeBytes;
            cache.socket.close(1009, 'Inbound message backlog exceeded');
            this.#handleSocketClose(cache);

            return;
        }

        queue.items.push({ message,
            bytes });
        queue.bytes += bytes;
        if (!queue.running) {
            queue.running = true;
            void this.#drainInbound(cache, queue);
        }
    }

    async #drainInbound (cache: SocketCache, queue: InboundQueue): Promise<void> {
        try {
            while (queue.items.length > 0 && this.#sockets.get(cache.clientId) === cache) {
                const item = queue.items.shift()!;
                queue.activeBytes = item.bytes;
                let timeout: ReturnType<typeof setTimeout> | null = null;
                let timedOut = false;
                const deadline = new Promise<void>((resolve) => {
                    timeout = setTimeout(() => {
                        timedOut = true;
                        queue.terminal = true;
                        queue.items.length = 0;
                        queue.bytes = queue.activeBytes;
                        cache.socket.close(1008, 'Message handler timed out');
                        this.#handleSocketClose(cache);
                        resolve();
                    }, this.#eventTimeout);
                    timeout.unref?.();
                });

                try {
                    await Promise.race([this.#handleMessage(cache, item.message), deadline]);
                } catch (error) {
                    this.sendMessage(cache.socket, this.#buildError(error));
                } finally {
                    if (timeout) {
                        clearTimeout(timeout);
                    }
                    queue.bytes = Math.max(0, queue.bytes - item.bytes);
                    queue.activeBytes = 0;
                }

                if (timedOut) {
                    queue.items.length = 0;
                    queue.bytes = 0;
                    break;
                }
            }
        } finally {
            queue.running = false;
            if (this.#inboundQueues.get(cache) === queue) {
                this.#inboundQueues.delete(cache);
            }
        }
    }

    #ensureOpen (): void {
        if (this.#closed) {
            throw new HttpError(503, `Endpoint ${this.path} is closed`);
        }
    }
}
