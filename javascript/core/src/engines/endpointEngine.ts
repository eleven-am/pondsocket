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
import { WebSocket } from 'ws';

import { LobbyEngine } from './lobbyEngine';
import { Middleware } from '../abstracts/middleware';
import { AuthorizationHandler, JoinRequestOptions, RequestCache, SocketCache } from '../abstracts/types';
import { JoinContext } from '../contexts/joinContext';
import { HttpError } from '../errors/httpError';
import { parseAddress } from '../matcher/matcher';
import { IDistributedBackend } from '../types';
import { PondChannel } from '../wrappers/pondChannel';


export class EndpointEngine {
    readonly #sockets: Map<string, SocketCache>;

    readonly #backend: IDistributedBackend | null;

    readonly #middleware: Middleware<RequestCache, JoinParams>;

    readonly #lobbyEngines: Map<PondPath<string>, LobbyEngine>;

    readonly #maxMessageSize: number;

    constructor (public readonly path: string, backend: IDistributedBackend | null, maxMessageSize: number = 1024 * 1024) {
        this.#sockets = new Map();
        this.#lobbyEngines = new Map();
        this.#middleware = new Middleware();
        this.#backend = backend || null;
        this.#maxMessageSize = maxMessageSize;
    }

    /**
     * Creates a new channel on a specified path
     */
    createChannel<Definition extends AnyPondChannelDefinition> (definition: Definition, handler: AuthorizationHandler<PathOf<Definition>, SchemaOf<Definition>>): PondChannel<SchemaOf<Definition>>;

    createChannel<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> (path: PondPath<Path>, handler: AuthorizationHandler<Path, Schema>): PondChannel<Schema>;

    createChannel<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> (pathOrDefinition: PondPath<Path> | AnyPondChannelDefinition, handler: AuthorizationHandler<Path, Schema>) {
        const path = isPondChannelDefinition(pathOrDefinition) ? pathOrDefinition.path : pathOrDefinition;

        if (typeof path === 'string') {
            compilePondRoute(path);
        }

        if (this.#lobbyEngines.has(path)) {
            throw new HttpError(409, `GatewayEngine: Channel pattern ${String(path)} is already registered`);
        }

        const lobbyEngine = new LobbyEngine(this, this.#backend);

        this.#middleware.use((user, joinParams, next) => {
            const event = parseAddress(path, user.channelName);

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
                const delegate = (error?: HttpError) => {
                    delegated = true;

                    return next(error);
                };

                return Promise.resolve(handler(context, delegate)).then(() => {
                    if (!delegated && !context.hasResponded) {
                        context.decline('Join handler completed without accepting or declining the request', 500);
                    }
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
        this.#sockets.set(cache.clientId, cache);
        cache.socket.on('message', (message: string) => this.#readMessage(cache, message));
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
        if (socket.readyState === WebSocket.OPEN) {
            socket.send(JSON.stringify(message));
        }
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
        try {
            this.#sockets.delete(cache.clientId);
            cache.subscriptions.forEach((unsubscribe) => unsubscribe());
            cache.channelSubscriptions?.clear();
        } catch {
            void 0;
        }
    }

    /**
     * Processes incoming messages from clients
     */
    #handleMessage (cache: SocketCache, message: ClientMessage) {
        switch (message.action) {
            case ClientActions.JOIN_CHANNEL:
                this.#joinChannel(message.channelName, cache, message.payload, message.requestId);
                break;

            case ClientActions.LEAVE_CHANNEL:
                this.#leaveChannel(message.channelName, cache);
                break;

            case ClientActions.BROADCAST:
                this.#broadcastMessage(cache, message);
                break;

            default:
                throw new Error(`GatewayEngine: Action ${message.action} does not exist`);
        }
    }

    /**
     * Handles a join channel request
     */
    #joinChannel (channel: string, socket: SocketCache, joinParams: JoinParams, requestId: string) {
        const cache: RequestCache = {
            ...socket,
            requestId,
            channelName: channel,
        };

        this.#middleware.run(cache, joinParams, (error) => {
            const requestError = error || new HttpError(404, `GatewayEngine: Channel ${channel} does not exist`);
            const errorType = error ? ErrorTypes.CHANNEL_ERROR : ErrorTypes.CHANNEL_NOT_FOUND;

            this.sendMessage(socket.socket, this.#buildError(requestError, errorType, channel, requestId));
        });
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

        const engine = this.#retrieveChannel(channel);

        engine.removeUser(socket.clientId);
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
    #broadcastMessage (socket: SocketCache, message: ClientMessage) {
        const engine = this.#retrieveChannel(message.channelName);

        engine.broadcastMessage(socket.clientId, message);
    }

    /**
     * Reads and processes a message from a WebSocket
     */
    #readMessage (cache: SocketCache, message: string) {
        try {
            if (message.length > this.#maxMessageSize) {
                throw new HttpError(413, `Message size ${message.length} exceeds maximum allowed size of ${this.#maxMessageSize} bytes`);
            }

            const data = JSON.parse(message);
            const result = clientMessageSchema.parse(data);

            this.#handleMessage(cache, result);
        } catch (e) {
            this.sendMessage(cache.socket, this.#buildError(e));
        }
    }
}
