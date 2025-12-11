import {
    BehaviorSubject,
    ChannelEvent,
    Events,
    JoinParams,
    ServerActions,
    Subject,
    channelEventSchema,
    ChannelState,
    Unsubscribe,
} from '@eleven-am/pondsocket-common';

import { Channel } from '../core/channel';
import { ClientMessage, ClientOptions, ConnectionState } from '../types';

const DEFAULT_CONNECTION_TIMEOUT = 10000;
const DEFAULT_MAX_RECONNECT_DELAY = 30000;

export interface SSEClientOptions extends ClientOptions {
    withCredentials?: boolean;
}

export class SSEClient {
    protected readonly _address: URL;

    protected readonly _postAddress: URL;

    protected readonly _options: Required<Omit<SSEClientOptions, 'pingInterval' | 'withCredentials'>> & Pick<SSEClientOptions, 'pingInterval' | 'withCredentials'>;

    protected _disconnecting: boolean;

    protected _eventSource: EventSource | undefined;

    protected _connectionId: string | undefined;

    protected _reconnectAttempts: number;

    protected _connectionTimeoutId: ReturnType<typeof setTimeout> | undefined;

    protected readonly _broadcaster: Subject<ChannelEvent>;

    protected readonly _connectionState: BehaviorSubject<ConnectionState>;

    protected readonly _errorSubject: Subject<Error>;

    #channels: Map<string, Channel>;

    constructor (endpoint: string, params: Record<string, any> = {}, options: SSEClientOptions = {}) {
        let address: URL;

        try {
            address = new URL(endpoint);
        } catch (e) {
            address = new URL(window.location.toString());
            address.pathname = endpoint;
        }

        this._disconnecting = false;
        this._reconnectAttempts = 0;
        const query = new URLSearchParams(params);

        address.search = query.toString();

        if (address.protocol !== 'https:' && address.protocol !== 'http:') {
            address.protocol = window.location.protocol;
        }

        this._address = address;
        this._postAddress = new URL(address.toString());
        this._options = {
            connectionTimeout: options.connectionTimeout ?? DEFAULT_CONNECTION_TIMEOUT,
            maxReconnectDelay: options.maxReconnectDelay ?? DEFAULT_MAX_RECONNECT_DELAY,
            pingInterval: options.pingInterval,
            withCredentials: options.withCredentials,
        };
        this.#channels = new Map();

        this._broadcaster = new Subject<ChannelEvent>();
        this._connectionState = new BehaviorSubject<ConnectionState>(ConnectionState.DISCONNECTED);
        this._errorSubject = new Subject<Error>();
        this.#init();
    }

    public connect () {
        this._disconnecting = false;
        this._connectionState.publish(ConnectionState.CONNECTING);

        const eventSource = new EventSource(this._address.toString(), {
            withCredentials: this._options.withCredentials ?? false,
        });

        this._connectionTimeoutId = setTimeout(() => {
            if (eventSource.readyState === EventSource.CONNECTING) {
                const error = new Error('Connection timeout');

                this._errorSubject.publish(error);
                eventSource.close();
            }
        }, this._options.connectionTimeout);

        eventSource.onopen = () => {
            this.#clearTimeout();
            this._reconnectAttempts = 0;
        };

        eventSource.onmessage = (event) => {
            this.#handleMessage(event.data);
        };

        eventSource.onerror = () => {
            const error = new Error('SSE connection error');

            this._errorSubject.publish(error);
            eventSource.close();

            this.#clearTimeout();
            this._connectionState.publish(ConnectionState.DISCONNECTED);

            if (this._disconnecting) {
                return;
            }

            const delay = Math.min(
                1000 * Math.pow(2, this._reconnectAttempts),
                this._options.maxReconnectDelay,
            );

            this._reconnectAttempts++;

            setTimeout(() => {
                this.connect();
            }, delay);
        };

        this._eventSource = eventSource;
    }

    #handleMessage (data: string) {
        try {
            const lines = data.trim().split('\n');

            for (const line of lines) {
                if (line.trim()) {
                    const parsed = JSON.parse(line);
                    const event = channelEventSchema.parse(parsed);

                    if (event.event === Events.CONNECTION && event.action === ServerActions.CONNECT) {
                        if (event.payload && typeof event.payload === 'object' && 'connectionId' in event.payload) {
                            this._connectionId = event.payload.connectionId as string;
                        }
                    }

                    this._broadcaster.publish(event);
                }
            }
        } catch (e) {
            this._errorSubject.publish(e instanceof Error ? e : new Error('Failed to parse SSE message'));
        }
    }

    #clearTimeout () {
        if (this._connectionTimeoutId) {
            clearTimeout(this._connectionTimeoutId);
            this._connectionTimeoutId = undefined;
        }
    }

    public getState () {
        return this._connectionState.value;
    }

    public getConnectionId () {
        return this._connectionId;
    }

    public disconnect () {
        this.#clearTimeout();
        this._disconnecting = true;
        this._connectionState.publish(ConnectionState.DISCONNECTED);

        if (this._connectionId) {
            fetch(this._postAddress.toString(), {
                method: 'DELETE',
                headers: {
                    'X-Connection-ID': this._connectionId,
                },
                credentials: this._options.withCredentials ? 'include' : 'same-origin',
            }).catch(() => {
            });
        }

        this._eventSource?.close();
        this._connectionId = undefined;
        this.#channels.clear();
    }

    public createChannel (name: string, params?: JoinParams) {
        const channel = this.#channels.get(name);

        if (channel && channel.channelState !== ChannelState.CLOSED) {
            return channel;
        }

        const publisher = this.#createPublisher();
        const newChannel = new Channel(publisher, this._connectionState, name, params || {});

        this.#channels.set(name, newChannel);

        return newChannel;
    }

    public onConnectionChange (callback: (state: ConnectionState) => void) {
        return this._connectionState.subscribe(callback);
    }

    public onError (callback: (error: Error) => void): Unsubscribe {
        return this._errorSubject.subscribe(callback);
    }

    #createPublisher () {
        return (message: ClientMessage) => {
            if (this._connectionState.value === ConnectionState.CONNECTED && this._connectionId) {
                fetch(this._postAddress.toString(), {
                    method: 'POST',
                    headers: {
                        'Content-Type': 'application/json',
                        'X-Connection-ID': this._connectionId,
                    },
                    body: JSON.stringify(message),
                    credentials: this._options.withCredentials ? 'include' : 'same-origin',
                }).catch((err) => {
                    this._errorSubject.publish(err instanceof Error ? err : new Error('Failed to send message'));
                });
            }
        };
    }

    #handleAcknowledge (message: ChannelEvent) {
        const channel = this.#channels.get(message.channelName) ?? new Channel(
            this.#createPublisher(),
            this._connectionState,
            message.channelName,
            {},
        );

        this.#channels.set(message.channelName, channel);
        channel.acknowledge(this._broadcaster);
    }

    #handleUnauthorized (message: ChannelEvent) {
        const channel = this.#channels.get(message.channelName);

        if (channel) {
            const payload = message.payload as { message?: string; statusCode?: number };

            channel.decline(payload);
        }
    }

    #init () {
        this._broadcaster.subscribe((message) => {
            if (message.event === Events.ACKNOWLEDGE) {
                this.#handleAcknowledge(message);
            } else if (message.event === Events.UNAUTHORIZED) {
                this.#handleUnauthorized(message);
            } else if (message.event === Events.CONNECTION && message.action === ServerActions.CONNECT) {
                this._connectionState.publish(ConnectionState.CONNECTED);
            }
        });
    }
}
