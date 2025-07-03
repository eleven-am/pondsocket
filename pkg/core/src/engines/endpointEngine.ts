import {
    ChannelEvent,
    ClientActions,
    ClientMessage,
    clientMessageSchema,
    ErrorTypes,
    Events,
    JoinParams,
    PondPath,
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

    constructor (public readonly path: string, backend: IDistributedBackend | null) {
        this.#sockets = new Map();
        this.#lobbyEngines = new Map();
        this.#middleware = new Middleware();
        this.#backend = backend || null;
    }

    /**
     * Creates a new channel on a specified path
     */
    createChannel<Path extends string> (path: PondPath<Path>, handler: AuthorizationHandler<Path>) {
        const lobbyEngine = new LobbyEngine(this, this.#backend);

        this.#middleware.use((user, joinParams, next) => {
            const event = parseAddress(path, user.channelName);

            if (event) {
                const options: JoinRequestOptions<Path> = {
                    clientId: user.clientId,
                    assigns: user.assigns,
                    params: event,
                    joinParams,
                };

                const channel = lobbyEngine.getOrCreateChannel(user.channelName);
                const context = new JoinContext(options, channel, user);

                return handler(context, next);
            }

            next();
        });

        this.#lobbyEngines.set(path, lobbyEngine);

        return new PondChannel(lobbyEngine);
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
        cache.socket.on('error', () => this.#handleSocketClose(cache));

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
        socket.send(JSON.stringify(message));
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
        } catch (e) {
            // no-op
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
            error = error || new HttpError(404, `GatewayEngine: Channel ${channel} does not exist`);
            throw error;
        });
    }

    /**
     * Handles a leave channel request
     */
    #leaveChannel (channel: string, socket: SocketCache) {
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
    #buildError (error: unknown) {
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
            event: ErrorTypes.INVALID_MESSAGE,
            action: ServerActions.ERROR,
            channelName: SystemSender.ENDPOINT,
            requestId: uuid(),
            payload: {
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
            const data = JSON.parse(message);
            const result = clientMessageSchema.parse(data);

            this.#handleMessage(cache, result);
        } catch (e) {
            this.sendMessage(cache.socket, this.#buildError(e));
        }
    }
}
