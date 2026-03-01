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

    constructor (endpoint: string, params: Record<string, any> = {}, options: SSEClientOptions = {}) {
        super(endpoint, params, options);
        this._postAddress = new URL(this._address.toString());
        this._withCredentials = options.withCredentials;
    }

    protected _resolveAddress (endpoint: string, params: Record<string, any>): URL {
        let address: URL;

        try {
            address = new URL(endpoint);
        } catch (e) {
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
        this._disconnecting = false;
        this._connectionId = undefined;
        this._connectionState.publish(ConnectionState.CONNECTING);

        const eventSource = new EventSource(this._address.toString(), {
            withCredentials: this._withCredentials ?? false,
        });

        this._connectionTimeoutId = setTimeout(() => {
            if (eventSource.readyState === EventSource.CONNECTING) {
                const error = new Error('Connection timeout');

                this._errorSubject.publish(error);
                eventSource.close();
            }
        }, this._options.connectionTimeout);

        eventSource.onopen = () => {
            this._clearConnectionTimeout();
            this._reconnectAttempts = 0;
        };

        eventSource.onmessage = (event) => {
            this.#handleMessage(event.data);
        };

        eventSource.onerror = () => {
            const error = new Error('SSE connection error');

            this._errorSubject.publish(error);
            eventSource.close();

            this._clearConnectionTimeout();
            this._connectionState.publish(ConnectionState.DISCONNECTED);
            this._scheduleReconnect();
        };

        this._eventSource = eventSource;
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
        this._disconnecting = true;
        this._connectionState.publish(ConnectionState.DISCONNECTED);

        if (this._connectionId) {
            fetch(this._postAddress.toString(), {
                method: 'DELETE',
                headers: {
                    'X-Connection-ID': this._connectionId,
                },
                credentials: this._withCredentials ? 'include' : 'same-origin',
            }).catch(() => {
            });
        }

        this._eventSource?.close();
        this._connectionId = undefined;
        this._clearChannels();
    }

    protected _createPublisher () {
        return (message: ClientMessage) => {
            if (this._connectionState.value === ConnectionState.CONNECTED && this._connectionId) {
                fetch(this._postAddress.toString(), {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-Connection-ID': this._connectionId,
                    },
                    body: JSON.stringify(message),
                    credentials: this._withCredentials ? 'include' : 'same-origin',
                }).catch((err) => {
                    this._errorSubject.publish(err instanceof Error ? err : new Error('Failed to send message'));
                });
            }
        };
    }
}
