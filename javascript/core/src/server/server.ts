import { IncomingHttpHeaders, IncomingMessage, Server as HTTPServer } from 'http';
import { Duplex } from 'node:stream';

import {
    compilePondRoute,
    IncomingConnection,
    isPondEndpointDefinition,
    PondEndpointDefinition,
    PondPath,
    uuid,
} from '@eleven-am/pondsocket-common';
import { WebSocket, WebSocketServer } from 'ws';

import { Middleware } from '../abstracts/middleware';
import { ConnectionHandler, ConnectionParams, ConnectionResponseOptions, SocketRequest } from '../abstracts/types';
import { ConnectionContext } from '../contexts/connectionContext';
import { EndpointEngine } from '../engines/endpointEngine';
import { parseAddress } from '../matcher/matcher';
import { IDistributedBackend, PondSocketOptions } from '../types';
import { Endpoint } from '../wrappers/endpoint';
import { rejectUpgrade } from './rejectUpgrade';

export class PondSocket {
    readonly #server: HTTPServer;

    readonly #exclusiveServer: boolean;

    readonly #socketServer: WebSocketServer;

    readonly #backend: IDistributedBackend | null;

    readonly #middleware: Middleware<SocketRequest, ConnectionParams>;

    readonly #maxMessageSize: number;

    readonly #maxBufferedAmount: number;

    readonly #heartbeatMs: number;

    readonly #connectionTimeout: number;

    readonly #joinTimeout: number;

    readonly #maxPendingJoins: number;

    readonly #maxPendingMessagesPerUser: number;

    readonly #maxPendingBytesPerUser: number;

    readonly #maxPendingMessagesPerSocket: number;

    readonly #maxPendingBytesPerSocket: number;

    readonly #eventTimeout: number;

    readonly #closeHttpServerOnShutdown: boolean;

    readonly #onError: ((error: Error) => void) | null;

    readonly #upgradeHandler: (req: IncomingMessage, socket: Duplex, head: Buffer) => void;

    readonly #serverErrorHandler = (error: Error) => {
        this.#serverError = error;

        if (this.#heartbeatTimer) {
            clearInterval(this.#heartbeatTimer);
            this.#heartbeatTimer = null;
        }
    };

    readonly #serverCloseHandler = () => {
        if (this.#heartbeatTimer) {
            clearInterval(this.#heartbeatTimer);
            this.#heartbeatTimer = null;
        }
    };

    #heartbeatTimer: ReturnType<typeof setInterval> | null = null;

    #backendReady: Promise<void> | null = null;

    #backendError: Error | null = null;

    #serverError: Error | null = null;

    #shutdownPromise: Promise<void> | null = null;

    readonly #endpoints: Map<string, EndpointEngine> = new Map();

    constructor ({
        server,
        socketServer,
        closeHttpServerOnShutdown = true,
        exclusiveServer = true,
        distributedBackend,
        maxMessageSize = 1024 * 1024,
        maxBufferedAmount = maxMessageSize * 4,
        heartbeatInterval = 30000,
        connectionTimeout = 10_000,
        joinTimeout = 10_000,
        maxPendingJoins = 32,
        maxPendingMessagesPerUser = 256,
        maxPendingBytesPerUser = maxBufferedAmount,
        maxPendingMessagesPerSocket = 64,
        maxPendingBytesPerSocket = maxMessageSize * 4,
        eventTimeout = 10_000,
        onError,
    }: PondSocketOptions = {}) {
        if (!Number.isSafeInteger(maxMessageSize) || maxMessageSize <= 0) {
            throw new TypeError('maxMessageSize must be a positive safe integer');
        }
        if (!Number.isSafeInteger(maxBufferedAmount) || maxBufferedAmount <= 0) {
            throw new TypeError('maxBufferedAmount must be a positive safe integer');
        }
        if (!Number.isSafeInteger(heartbeatInterval) || heartbeatInterval <= 0) {
            throw new TypeError('heartbeatInterval must be a positive safe integer');
        }
        if (!Number.isSafeInteger(connectionTimeout) || connectionTimeout <= 0) {
            throw new TypeError('connectionTimeout must be a positive safe integer');
        }
        if (!Number.isSafeInteger(joinTimeout) || joinTimeout <= 0) {
            throw new TypeError('joinTimeout must be a positive safe integer');
        }
        if (!Number.isSafeInteger(maxPendingJoins) || maxPendingJoins <= 0) {
            throw new TypeError('maxPendingJoins must be a positive safe integer');
        }
        for (const [name, value] of Object.entries({
            maxPendingMessagesPerUser,
            maxPendingBytesPerUser,
            maxPendingMessagesPerSocket,
            maxPendingBytesPerSocket,
            eventTimeout,
        })) {
            if (!Number.isSafeInteger(value) || value <= 0) {
                throw new TypeError(`${name} must be a positive safe integer`);
            }
        }

        this.#middleware = new Middleware();
        this.#exclusiveServer = exclusiveServer;
        this.#server = server ?? new HTTPServer();
        this.#backend = distributedBackend ?? null;
        this.#maxMessageSize = maxMessageSize;
        this.#maxBufferedAmount = maxBufferedAmount;
        this.#heartbeatMs = heartbeatInterval;
        this.#connectionTimeout = connectionTimeout;
        this.#joinTimeout = joinTimeout;
        this.#maxPendingJoins = maxPendingJoins;
        this.#maxPendingMessagesPerUser = maxPendingMessagesPerUser;
        this.#maxPendingBytesPerUser = maxPendingBytesPerUser;
        this.#maxPendingMessagesPerSocket = maxPendingMessagesPerSocket;
        this.#maxPendingBytesPerSocket = maxPendingBytesPerSocket;
        this.#eventTimeout = eventTimeout;
        this.#closeHttpServerOnShutdown = closeHttpServerOnShutdown;
        this.#onError = onError ?? null;
        this.#socketServer = socketServer ?? new WebSocketServer({ noServer: true,
            maxPayload: maxMessageSize });
        this.#upgradeHandler = this.#handleUpgrade.bind(this);
        this.#init();
    }

    /**
     * Start listening for connections
     */
    listen (...args: any[]) {
        return this.#server.listen(...args);
    }

    /**
     * Close the server
     */
    close (callback?: (err?: Error) => void, timeout: number = 5000) {
        this.closeAsync(timeout)
            .then(() => callback?.())
            .catch((error) => callback?.(error instanceof Error ? error : new Error(String(error))));

        return this.#server;
    }

    closeAsync (timeout: number = 5000): Promise<void> {
        if (this.#shutdownPromise) {
            return this.#shutdownPromise;
        }

        this.#shutdownPromise = this.#shutdown(timeout);

        return this.#shutdownPromise;
    }

    async ready (): Promise<void> {
        await (this.#backendReady ?? Promise.resolve());

        if (this.#backendError) {
            throw this.#backendError;
        }
    }

    isHealthy (): boolean {
        const backendHealthy = this.#backend?.isHealthy?.() ?? true;

        return this.#backendError === null && this.#serverError === null && this.#shutdownPromise === null && backendHealthy;
    }

    async #shutdown (timeout: number): Promise<void> {
        if (this.#heartbeatTimer) {
            clearInterval(this.#heartbeatTimer);
            this.#heartbeatTimer = null;
        }

        this.#server.off('upgrade', this.#upgradeHandler);
        this.#server.off('error', this.#serverErrorHandler);
        this.#server.off('close', this.#serverCloseHandler);

        const endpoints = [...this.#endpoints.values()];

        this.#endpoints.clear();
        endpoints.forEach((endpoint) => endpoint.close());

        this.#socketServer.clients.forEach((socket) => {
            if (socket.readyState === WebSocket.OPEN) {
                socket.close(1001, 'Server shutting down');
            }
        });

        const closeSockets = new Promise<void>((resolve) => {
            const forceClose = setTimeout(() => {
                this.#socketServer.clients.forEach((socket) => {
                    if (socket.readyState !== WebSocket.CLOSED) {
                        socket.terminate();
                    }
                });
                resolve();
            }, timeout);

            forceClose.unref?.();

            try {
                this.#socketServer.close(() => {
                    clearTimeout(forceClose);
                    resolve();
                });
            } catch {
                clearTimeout(forceClose);
                resolve();
            }
        });
        const cleanupBackend = this.#backend
            ? Promise.resolve().then(() => this.#backend!.cleanup())
            : Promise.resolve();
        const closeHttpServer = this.#closeHttpServerOnShutdown && this.#server.listening
            ? new Promise<void>((resolve, reject) => {
                this.#server.close((error) => error ? reject(error) : resolve());
            })
            : Promise.resolve();

        await Promise.all([closeSockets, cleanupBackend, closeHttpServer]);
    }

    /**
     * Create a new endpoint
     */
    createEndpoint<Path extends string> (definition: PondEndpointDefinition<Path>, handler: ConnectionHandler<Path>): Endpoint;

    createEndpoint<Path extends string> (path: PondPath<Path>, handler: ConnectionHandler<Path>): Endpoint;

    createEndpoint<Path extends string> (pathOrDefinition: PondPath<Path> | PondEndpointDefinition<Path>, handler: ConnectionHandler<Path>) {
        if (this.#shutdownPromise) {
            throw new Error('Cannot create an endpoint after shutdown has started');
        }

        const path = isPondEndpointDefinition(pathOrDefinition) ? pathOrDefinition.path as Path : pathOrDefinition;

        if (typeof path === 'string') {
            compilePondRoute(path);
        }

        const endpointPath = String(path);

        if (this.#endpoints.has(endpointPath)) {
            throw new Error(`Endpoint ${endpointPath} is already registered`);
        }

        const endpoint = new EndpointEngine(
            endpointPath,
            this.#backend,
            this.#maxMessageSize,
            this.#maxBufferedAmount,
            this.#onError,
            this.#joinTimeout,
            this.#maxPendingJoins,
            this.#maxPendingMessagesPerUser,
            this.#maxPendingBytesPerUser,
            this.#maxPendingMessagesPerSocket,
            this.#maxPendingBytesPerSocket,
            this.#eventTimeout,
        );

        this.#middleware.use((req, params, next) => {
            const event = parseAddress(path, req.address);

            if (!event) {
                return next();
            }

            const request: IncomingConnection<Path> = {
                ...event,
                cookies: this.#getCookies(req.headers),
                headers: req.headers,
                address: req.address,
                id: req.id,
            };

            const newParams: ConnectionResponseOptions = {
                params,
                engine: endpoint,
                webSocketServer: this.#socketServer,
            };

            const context = new ConnectionContext<Path>(request, newParams);

            let delegated = false;
            const delegate = (error?: Parameters<typeof next>[0]) => {
                delegated = true;

                return next(error);
            };

            return Promise.resolve(handler(context, delegate)).then(() => {
                if (!delegated && !context.hasResponded) {
                    context.decline('Connection handler completed without accepting or declining the request', 500);
                }
            });
        });

        this.#endpoints.set(endpointPath, endpoint);

        return new Endpoint(endpoint);
    }

    /**
     * Handle WebSocket upgrade requests
     */
    #handleUpgrade (req: IncomingMessage, socket: Duplex, head: Buffer) {
        const clientId = uuid();
        let settled = false;
        let handleSocketClose = () => {};
        const settle = () => {
            if (settled) {
                return false;
            }
            settled = true;
            clearTimeout(timeout);
            socket.off?.('close', handleSocketClose);

            return true;
        };
        const timeout = setTimeout(() => {
            if (settle()) {
                rejectUpgrade(socket, 408, 'Connection authorization timed out');
            }
        }, this.#connectionTimeout);

        timeout.unref?.();
        handleSocketClose = () => {
            settle();
        };
        socket.once?.('close', handleSocketClose);
        const request: SocketRequest = {
            id: clientId,
            headers: req.headers,
            address: req.url || '',
        };

        const params: ConnectionParams = {
            head,
            socket,
            request: req,
            requestId: uuid(),
            settle,
        };

        this.#middleware.run(request, params, (error) => {
            if (!settle()) {
                return;
            }

            if (error) {
                const code = error?.statusCode || 400;
                const isInternal = code >= 500;
                const message = isInternal ? 'Internal server error' : error?.message || 'Unauthorized connection';

                if (isInternal) {
                    this.#reportError(error, 'Internal connection handler error');
                }

                rejectUpgrade(socket, code, message);
            } else if (this.#exclusiveServer) {
                rejectUpgrade(socket, 404, 'Not Found');
            }
        });
    }

    /**
     * Set up WebSocket heartbeat
     */
    #manageHeartbeat () {
        this.#socketServer.on('connection', (socket: WebSocket & { isAlive?: boolean }) => {
            socket.on('pong', () => {
                socket.isAlive = true;
            });
        });

        const timer = setInterval(() => {
            this.#socketServer.clients.forEach((socket: WebSocket & { isAlive?: boolean }) => {
                if (socket.isAlive === false) {
                    return socket.terminate();
                }

                socket.isAlive = false;
                socket.ping();
            });
        }, this.#heartbeatMs);

        timer.unref?.();

        return timer;
    }

    /**
     * Initialize the server
     */
    #init () {
        this.#heartbeatTimer = this.#manageHeartbeat();
        this.#backendReady = this.#backend?.initialize().catch((error) => {
            this.#backendError = error instanceof Error ? error : new Error(String(error));
        }) ?? null;

        this.#server.on('error', this.#serverErrorHandler);
        this.#server.on('close', this.#serverCloseHandler);

        this.#server.on('upgrade', this.#upgradeHandler);
    }

    /**
     * Parse cookies from headers
     */
    #getCookies (headers: IncomingHttpHeaders): Record<string, string> {
        const cookieHeader = headers.cookie;

        if (!cookieHeader) {
            return {};
        }

        const entries = cookieHeader.split(';')
            .reduce<Array<[string, string]>>((cookies, cookie) => {
                const trimmed = cookie.trim();
                const eqIndex = trimmed.indexOf('=');

                if (eqIndex === -1) {
                    return cookies;
                }

                const name = trimmed.slice(0, eqIndex);
                const value = trimmed.slice(eqIndex + 1);

                try {
                    cookies.push([name, decodeURIComponent(value)]);
                } catch {
                    cookies.push([name, value]);
                }

                return cookies;
            }, []);

        return Object.fromEntries(entries);
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
