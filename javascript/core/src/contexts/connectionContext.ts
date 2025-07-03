import { IncomingHttpHeaders } from 'http';

import {
    PondAssigns,
    PondMessage,
    ChannelEvent,
    SystemSender,
    ServerActions,
    IncomingConnection,
    Params,
    EventParams,
} from '@eleven-am/pondsocket-common';
import { WebSocketServer } from 'ws';

import { ConnectionParams, SocketCache, ConnectionResponseOptions } from '../abstracts/types';
import { EndpointEngine } from '../engines/endpointEngine';
import { HttpError } from '../errors/httpError';

/**
 * ConnectionContext combines IncomingConnection data with ConnectionResponse functionality.
 */
export class ConnectionContext<Path extends string> {
    #executed: boolean;

    #assigns: PondAssigns;

    readonly #clientId: string;

    readonly #engine: EndpointEngine;

    readonly #params: ConnectionParams;

    readonly #webSocketServer: WebSocketServer;

    readonly #connectionData: IncomingConnection<Path>;

    constructor (
        connectionData: IncomingConnection<Path>,
        { engine, params, webSocketServer }: ConnectionResponseOptions,
    ) {
        this.#webSocketServer = webSocketServer;
        this.#clientId = connectionData.id;
        this.#executed = false;
        this.#engine = engine;
        this.#params = params;
        this.#assigns = {};
        this.#connectionData = connectionData;
    }

    /**
     * Whether the connection has been handled
     */
    get hasResponded (): boolean {
        return this.#executed;
    }

    /**
     * Connection details including headers, cookies, address, id, etc.
     */
    get connection (): IncomingConnection<Path> {
        return this.#connectionData;
    }

    /**
     * The request parameters
     */
    get requestId (): string {
        return this.#params.requestId;
    }

    /**
     * The client ID
     */
    get clientId (): string {
        return this.#clientId;
    }

    /**
     * The request headers
     */
    get headers (): IncomingHttpHeaders {
        return this.#connectionData.headers;
    }

    /**
     * The request cookies
     */
    get cookies (): Record<string, string> {
        return this.#connectionData.cookies;
    }

    /**
     * The client address
     */
    get address (): string {
        return this.#connectionData.address;
    }

    /**
     * The event parameters
     */
    get event (): EventParams<Path> {
        return {
            query: this.#connectionData.query,
            params: this.#connectionData.params,
        };
    }

    /**
     * The query parameters
     */
    get query (): Record<string, string> {
        return this.#connectionData.query;
    }

    /**
     * The path parameters
     */
    get params (): Params<Path> {
        return this.#connectionData.params;
    }

    /**
     * Assigns data to the client
     */
    assign (assigns: PondAssigns): ConnectionContext<Path> {
        if (!this.#executed) {
            this.#assigns = {
                ...this.#assigns,
                ...assigns,
            };
        } else {
            const user = this.#engine.getUser(this.#clientId);

            user.assigns = {
                ...user.assigns,
                ...assigns,
            };
        }

        return this;
    }

    /**
     * Accepts the connection request
     */
    accept (): ConnectionContext<Path> {
        this.#performChecks();

        this.#webSocketServer.handleUpgrade(
            this.#params.request,
            this.#params.socket,
            this.#params.head,
            (ws) => {
                const cache: SocketCache = {
                    clientId: this.#clientId,
                    subscriptions: new Set(),
                    assigns: this.#assigns,
                    socket: ws,
                };

                this.#webSocketServer.emit('connection', ws);
                this.#engine.manageSocket(cache);
            },
        );

        return this;
    }

    /**
     * Declines the connection request
     */
    decline (message?: string, errorCode?: number): ConnectionContext<Path> {
        this.#performChecks();
        const code = errorCode || 400;
        const { socket } = this.#params;
        const newMessage = message || 'Unauthorized connection';

        socket.write(`HTTP/1.1 ${code} ${newMessage}\r\n\r\n`);
        socket.destroy();

        return this;
    }

    /**
     * Sends a direct reply to the client
     */
    reply (event: string, payload: PondMessage): ConnectionContext<Path> {
        if (!this.#executed) {
            throw new HttpError(400, 'Connection has not been accepted');
        }

        const { socket } = this.#engine.getUser(this.#clientId);

        const message: ChannelEvent = {
            event,
            payload,
            action: ServerActions.BROADCAST,
            requestId: this.#params.requestId,
            channelName: SystemSender.ENDPOINT,
        };

        this.#engine.sendMessage(socket, message);

        return this;
    }

    /**
     * Performs checks before handling the request
     */
    #performChecks (): void {
        if (this.#executed) {
            throw new HttpError(400, 'Response has already been executed');
        }

        this.#executed = true;
    }
}
