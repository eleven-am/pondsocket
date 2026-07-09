import { IncomingHttpHeaders, IncomingMessage, Server as HTTPServer } from 'http';
import internal from 'node:stream';

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

export class PondSocket {
    readonly #server: HTTPServer;

    readonly #exclusiveServer: boolean;

    readonly #socketServer: WebSocketServer;

    readonly #backend: IDistributedBackend | null;

    readonly #middleware: Middleware<SocketRequest, ConnectionParams>;

    readonly #maxMessageSize: number;

    readonly #heartbeatMs: number;

    readonly #closeHttpServerOnShutdown: boolean;

    readonly #upgradeHandler: (req: IncomingMessage, socket: internal.Duplex, head: Buffer) => void;

    #heartbeatTimer: ReturnType<typeof setInterval> | null = null;

    #backendReady: Promise<void> | null = null;

    #backendError: Error | null = null;

    #serverError: Error | null = null;

    #shutdownPromise: Promise<void> | null = null;

    constructor ({
        server,
        socketServer,
        closeHttpServerOnShutdown = true,
        exclusiveServer = true,
        distributedBackend,
        maxMessageSize = 1024 * 1024,
        heartbeatInterval = 30000,
    }: PondSocketOptions = {}) {
        this.#middleware = new Middleware();
        this.#exclusiveServer = exclusiveServer;
        this.#server = server ?? new HTTPServer();
        this.#backend = distributedBackend ?? null;
        this.#maxMessageSize = maxMessageSize;
        this.#heartbeatMs = heartbeatInterval;
        this.#closeHttpServerOnShutdown = closeHttpServerOnShutdown;
        this.#socketServer = socketServer ?? new WebSocketServer({ noServer: true });
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
        return this.#backendError === null && this.#serverError === null && this.#shutdownPromise === null;
    }

    async #shutdown (timeout: number): Promise<void> {
        if (this.#heartbeatTimer) {
            clearInterval(this.#heartbeatTimer);
            this.#heartbeatTimer = null;
        }

        this.#server.off('upgrade', this.#upgradeHandler);

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
        const cleanupBackend = (this.#backendReady ?? Promise.resolve())
            .catch(() => {})
            .then(() => this.#backend?.cleanup());
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
        const path = isPondEndpointDefinition(pathOrDefinition) ? pathOrDefinition.path as Path : pathOrDefinition;

        if (typeof path === 'string') {
            compilePondRoute(path);
        }

        const endpoint = new EndpointEngine(String(path), this.#backend, this.#maxMessageSize);

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

        return new Endpoint(endpoint);
    }

    /**
     * Handle WebSocket upgrade requests
     */
    #handleUpgrade (req: IncomingMessage, socket: internal.Duplex, head: Buffer) {
        const clientId = uuid();
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
        };

        this.#middleware.run(request, params, (error) => {
            if (error) {
                const code = error?.statusCode || 400;
                const message = error?.message || 'Unauthorized connection';

                socket.write(`HTTP/1.1 ${code} ${message}\r\n\r\n`);
                socket.destroy();
            } else if (this.#exclusiveServer) {
                socket.write('HTTP/1.1 404 Not Found\r\n\r\n');
                socket.destroy();
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

        this.#server.on('error', (error) => {
            this.#serverError = error;

            if (this.#heartbeatTimer) {
                clearInterval(this.#heartbeatTimer);
                this.#heartbeatTimer = null;
            }
        });

        this.#server.on('close', () => {
            if (this.#heartbeatTimer) {
                clearInterval(this.#heartbeatTimer);
                this.#heartbeatTimer = null;
            }
        });

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

        return cookieHeader.split(';')
            .reduce((cookies, cookie) => {
                const trimmed = cookie.trim();
                const eqIndex = trimmed.indexOf('=');

                if (eqIndex === -1) {
                    return cookies;
                }

                const name = trimmed.slice(0, eqIndex);
                const value = trimmed.slice(eqIndex + 1);

                try {
                    cookies[name] = decodeURIComponent(value);
                } catch {
                    cookies[name] = value;
                }

                return cookies;
            }, {} as Record<string, string>);
    }
}
