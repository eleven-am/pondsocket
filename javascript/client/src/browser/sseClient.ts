import { ClientMessage, Events, ServerActions } from '@eleven-am/pondsocket-common';

import { BaseClient } from '../core/baseClient';
import { ClientOptions, ConnectionState, TransportPublishError, utf8ByteLength } from '../types';

export interface SSEClientOptions extends ClientOptions {
    withCredentials?: boolean;
}

export class SSEClient extends BaseClient {
    protected readonly _postAddress: URL;

    protected readonly _withCredentials: boolean | undefined;

    protected _eventSource: EventSource | undefined;

    protected _connectionId: string | undefined;

    protected _publishChain: Promise<void> = Promise.resolve();

    protected _requestGeneration: number = 0;

    protected _pendingRequests: number = 0;

    readonly #activeRequestControllers: Set<AbortController> = new Set();

    constructor (endpoint: string, params: Record<string, any> = {}, options: SSEClientOptions = {}) {
        if (typeof EventSource === 'undefined') {
            throw new Error(
                'SSEClient requires the browser EventSource API; use PondClient in Node.js or install an EventSource polyfill',
            );
        }
        super(endpoint, params, options);
        this._postAddress = new URL(this._address.toString());
        this._withCredentials = options.withCredentials;
    }

    protected _resolveAddress (endpoint: string, params: Record<string, any>): URL {
        let address: URL;

        try {
            address = new URL(endpoint);
        } catch {
            address = new URL(window.location.toString());
            address.pathname = endpoint;
        }

        const query = new URLSearchParams(params);

        address.search = query.toString();

        if (address.protocol !== 'https:' && address.protocol !== 'http:') {
            address.protocol = window.location.protocol;
        }

        return address;
    }

    public connect () {
        if (!this._disconnecting && this._eventSource &&
            (this._eventSource.readyState === EventSource.CONNECTING || this._eventSource.readyState === EventSource.OPEN)) {
            return;
        }

        this._clearReconnectTimeout();
        this._clearConnectionTimeout();
        this.#invalidatePendingRequests();
        this._disconnecting = false;
        this._connectionId = undefined;
        this._connectionState.publish(ConnectionState.CONNECTING);

        const previousEventSource = this._eventSource;
        const eventSource = new EventSource(this._address.toString(), {
            withCredentials: this._withCredentials ?? false,
        });

        this._eventSource = eventSource;
        if (previousEventSource && previousEventSource !== eventSource) {
            previousEventSource.close();
        }

        this._connectionTimeoutId = setTimeout(() => {
            if (this._eventSource === eventSource && this._connectionState.value !== ConnectionState.CONNECTED) {
                this.#closeEventSource(eventSource, new Error('Connection timeout'));
            }
        }, this._options.connectionTimeout);
        this._unrefTimer(this._connectionTimeoutId);

        eventSource.onopen = () => {
            if (this._eventSource !== eventSource) {
                return;
            }

            // The transport is not usable until PondSocket sends a valid
            // CONNECTION event with a connection id. The protocol timeout
            // intentionally remains armed until then.
        };

        eventSource.onmessage = (event) => {
            if (this._eventSource !== eventSource) {
                return;
            }

            if (!this._acceptInboundData(event.data)) {
                this.#closeEventSource(eventSource);

                return;
            }

            this.#handleMessage(event.data, eventSource);
        };

        eventSource.onerror = () => {
            if (this._eventSource !== eventSource) {
                return;
            }

            this.#closeEventSource(eventSource, new Error('SSE connection error'));
        };
    }

    #handleMessage (data: string, eventSource: EventSource) {
        const events = this._parseMessages(data);

        for (const event of events) {
            if (event.event === Events.CONNECTION && event.action === ServerActions.CONNECT) {
                const connectionId = event.payload && typeof event.payload === 'object' && 'connectionId' in event.payload
                    ? event.payload.connectionId
                    : undefined;

                if (typeof connectionId !== 'string' ||
                    connectionId.length === 0 ||
                    connectionId.length > 512 ||
                    connectionId.trim() !== connectionId ||
                    (this._connectionId !== undefined && this._connectionId !== connectionId)) {
                    this.#closeEventSource(eventSource, new Error('SSE CONNECTION event has an invalid connection id'));

                    return;
                }

                this._connectionId = connectionId;
            }

            this._broadcaster.publish(event);
        }
    }

    public getConnectionId () {
        return this._connectionId;
    }

    public disconnect () {
        this._clearConnectionTimeout();
        this._clearReconnectTimeout();
        this._disconnecting = true;
        this._connectionState.publish(ConnectionState.DISCONNECTED);

        const eventSource = this._eventSource;
        const connectionId = this._connectionId;

        this._eventSource = undefined;
        this._connectionId = undefined;
        eventSource?.close();
        this.#invalidatePendingRequests();

        if (connectionId) {
            this.#deleteConnection(connectionId);
        }
        this._clearChannels();
    }

    protected _createPublisher () {
        return (message: ClientMessage, serialized?: { data: string; byteLength: number }) => {
            const connectionId = this._connectionId;
            const eventSource = this._eventSource;

            if (this._connectionState.value !== ConnectionState.CONNECTED || !connectionId) {
                throw new TransportPublishError('Cannot send message while the SSE client is not connected', true);
            }

            const generation = this._requestGeneration;
            let body: string;

            try {
                body = serialized?.data ?? JSON.stringify(message);
            } catch (error) {
                throw new TransportPublishError('Message cannot be serialized as JSON', false, error);
            }
            const byteLength = serialized?.byteLength ?? utf8ByteLength(body);

            if (byteLength > this._options.maxMessageSize) {
                throw new TransportPublishError(
                    `Outbound message exceeds maximum size of ${this._options.maxMessageSize} bytes`,
                    false,
                );
            }

            try {
                return this.#enqueueRequest((signal) => fetch(this._postAddress.toString(), {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-Connection-ID': connectionId,
                    },
                    body,
                    credentials: this._withCredentials ? 'include' : 'same-origin',
                    signal,
                }), 'Failed to send SSE message', generation).catch((error) => {
                    if (eventSource && this._eventSource === eventSource && generation === this._requestGeneration) {
                        this.#closeEventSource(eventSource);
                    }

                    throw new TransportPublishError(
                        'SSE delivery failed and was not replayed because delivery may be ambiguous',
                        false,
                        error,
                    );
                });
            } catch (error) {
                if (eventSource && this._eventSource === eventSource) {
                    this.#closeEventSource(eventSource);
                }

                throw new TransportPublishError(
                    `SSE request could not be queued; reconnecting before retry: ${error instanceof Error ? error.message : String(error)}`,
                    true,
                    error,
                );
            }
        };
    }

    #enqueueRequest (
        request: (signal: AbortSignal) => Promise<Response>,
        failureMessage: string,
        generation: number,
    ) {
        if (this._pendingRequests >= this._options.maxPendingRequests) {
            const error = new Error(`SSE request queue capacity of ${this._options.maxPendingRequests} was exceeded`);

            this._errorSubject.publish(error);
            throw error;
        }

        this._pendingRequests++;
        const delivery = this._publishChain
            .then(async () => {
                if (generation !== this._requestGeneration) {
                    throw new Error(`${failureMessage}: connection changed before delivery`);
                }

                const controller = new AbortController();
                let timedOut = false;
                const timeoutId = setTimeout(() => {
                    timedOut = true;
                    controller.abort();
                }, this._options.requestTimeout);

                this._unrefTimer(timeoutId);
                this.#activeRequestControllers.add(controller);

                try {
                    const response = await request(controller.signal);

                    if (!response.ok) {
                        throw new Error(`${failureMessage}: HTTP ${response.status}`);
                    }
                } catch (error) {
                    if (generation !== this._requestGeneration) {
                        const cancellationError = new Error(
                            `${failureMessage}: connection changed during delivery`,
                        ) as Error & { cause?: unknown };

                        cancellationError.cause = error;
                        throw cancellationError;
                    }

                    if (timedOut) {
                        const timeoutError = new Error(
                            `${failureMessage}: timed out after ${this._options.requestTimeout}ms`,
                        ) as Error & { cause?: unknown };

                        timeoutError.cause = error;
                        throw timeoutError;
                    }

                    throw error;
                } finally {
                    clearTimeout(timeoutId);
                    this.#activeRequestControllers.delete(controller);
                }
            });

        this._publishChain = delivery.catch((error) => {
                this._errorSubject.publish(error instanceof Error ? error : new Error(failureMessage));
            })
            .finally(() => {
                this._pendingRequests--;
            });

        return delivery;
    }

    #invalidatePendingRequests () {
        this._requestGeneration++;
        this.#activeRequestControllers.forEach((controller) => controller.abort());
        this.#activeRequestControllers.clear();
    }

    #closeEventSource (eventSource: EventSource, error?: Error) {
        if (this._eventSource !== eventSource) {
            return;
        }

        if (error) {
            this._errorSubject.publish(error);
        }

        this._eventSource = undefined;
        this._connectionId = undefined;
        this._clearConnectionTimeout();
        this.#invalidatePendingRequests();
        eventSource.close();
        this._connectionState.publish(ConnectionState.DISCONNECTED);
        this._scheduleReconnect();
    }

    #deleteConnection (connectionId: string) {
        const controller = new AbortController();
        const timeoutId = setTimeout(() => controller.abort(), this._options.requestTimeout);

        this._unrefTimer(timeoutId);
        void fetch(this._postAddress.toString(), {
            method: 'DELETE',
            headers: {
                'X-Connection-ID': connectionId,
            },
            credentials: this._withCredentials ? 'include' : 'same-origin',
            signal: controller.signal,
        }).then((response) => {
            if (!response.ok) {
                throw new Error(`Failed to close SSE connection: HTTP ${response.status}`);
            }
        }).catch((error) => {
            this._errorSubject.publish(error instanceof Error ? error : new Error('Failed to close SSE connection'));
        }).finally(() => clearTimeout(timeoutId));
    }
}
