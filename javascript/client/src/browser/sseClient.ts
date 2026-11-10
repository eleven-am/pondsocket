import { ClientMessage, Events, ServerActions } from '@eleven-am/pondsocket-common';

import { BaseClient } from '../core/baseClient';
import { ClientOptions, ConnectionState } from '../types';

export interface SSEClientOptions extends ClientOptions {
    withCredentials?: boolean;
}

export class SSEClient extends BaseClient {
    protected readonly _postAddress: URL;

    protected readonly _withCredentials: boolean | undefined;

    protected _eventSource: EventSource | undefined;

    protected _connectionId: string | undefined;

    protected _publishChain: Promise<void> = Promise.resolve();

    constructor (endpoint: string, params: Record<string, any> = {}, options: SSEClientOptions = {}) {
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
            if (this._eventSource === eventSource && eventSource.readyState === EventSource.CONNECTING) {
                const error = new Error('Connection timeout');

                this._errorSubject.publish(error);
                this._eventSource = undefined;
                eventSource.close();
                this._connectionState.publish(ConnectionState.DISCONNECTED);
                this._scheduleReconnect();
            }
        }, this._options.connectionTimeout);
        this._unrefTimer(this._connectionTimeoutId);

        eventSource.onopen = () => {
            if (this._eventSource !== eventSource) {
                return;
            }

            this._clearConnectionTimeout();
        };

        eventSource.onmessage = (event) => {
            if (this._eventSource !== eventSource) {
                return;
            }

            this.#handleMessage(event.data);
        };

        eventSource.onerror = () => {
            if (this._eventSource !== eventSource) {
                return;
            }

            const error = new Error('SSE connection error');

            this._errorSubject.publish(error);
            eventSource.close();
            this._eventSource = undefined;

            this._clearConnectionTimeout();
            this._connectionState.publish(ConnectionState.DISCONNECTED);
            this._scheduleReconnect();
        };
    }

    #handleMessage (data: string) {
        try {
            const events = this._parseMessages(data);

            for (const event of events) {
                if (event.event === Events.CONNECTION && event.action === ServerActions.CONNECT) {
                    if (event.payload && typeof event.payload === 'object' && 'connectionId' in event.payload) {
                        this._connectionId = event.payload.connectionId as string;
                    }
                }

                this._broadcaster.publish(event);
            }
        } catch (e) {
            this._errorSubject.publish(e instanceof Error ? e : new Error('Failed to parse SSE message'));
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

        if (this._connectionId) {
            const connectionId = this._connectionId;

            this.#enqueueRequest(() => fetch(this._postAddress.toString(), {
                method: 'DELETE',
                headers: {
                    'X-Connection-ID': connectionId,
                },
                credentials: this._withCredentials ? 'include' : 'same-origin',
            }), 'Failed to close SSE connection');
        }

        const eventSource = this._eventSource;

        this._eventSource = undefined;
        eventSource?.close();
        this._connectionId = undefined;
        this._clearChannels();
    }

    protected _createPublisher () {
        return (message: ClientMessage) => {
            const connectionId = this._connectionId;

            if (this._connectionState.value !== ConnectionState.CONNECTED || !connectionId) {
                throw new Error('Cannot send message while the SSE client is not connected');
            }

            this.#enqueueRequest(() => fetch(this._postAddress.toString(), {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-Connection-ID': connectionId,
                    },
                    body: JSON.stringify(message),
                    credentials: this._withCredentials ? 'include' : 'same-origin',
                }), 'Failed to send SSE message');
        };
    }

    #enqueueRequest (request: () => Promise<Response>, failureMessage: string) {
        this._publishChain = this._publishChain
            .then(async () => {
                const response = await request();

                if (!response.ok) {
                    throw new Error(`${failureMessage}: HTTP ${response.status}`);
                }
            })
            .catch((error) => {
                this._errorSubject.publish(error instanceof Error ? error : new Error(failureMessage));
            });
    }
}
