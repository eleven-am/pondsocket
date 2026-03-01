import {
    BehaviorSubject,
    ChannelEvent,
    channelEventSchema,
    Events,
    JoinParams,
    ServerActions,
    Subject,
    ChannelState,
    Unsubscribe,
} from '@eleven-am/pondsocket-common';

import { Channel } from './channel';
import { ClientMessage, ClientOptions, ConnectionState } from '../types';

const DEFAULT_CONNECTION_TIMEOUT = 10000;
const DEFAULT_MAX_RECONNECT_DELAY = 30000;

export abstract class BaseClient {
    protected readonly _address: URL;

    protected readonly _options: Required<Omit<ClientOptions, 'pingInterval'>> & Pick<ClientOptions, 'pingInterval'>;

    protected _disconnecting: boolean;

    protected _reconnectAttempts: number;

    protected _connectionTimeoutId: ReturnType<typeof setTimeout> | undefined;

    protected readonly _broadcaster: Subject<ChannelEvent>;

    protected readonly _connectionState: BehaviorSubject<ConnectionState>;

    protected readonly _errorSubject: Subject<Error>;

    readonly #channels: Map<string, Channel>;

    constructor (endpoint: string, params: Record<string, any> = {}, options: ClientOptions = {}) {
        this._address = this._resolveAddress(endpoint, params);
        this._disconnecting = false;
        this._reconnectAttempts = 0;
        this._options = {
            connectionTimeout: options.connectionTimeout ?? DEFAULT_CONNECTION_TIMEOUT,
            maxReconnectDelay: options.maxReconnectDelay ?? DEFAULT_MAX_RECONNECT_DELAY,
            pingInterval: options.pingInterval,
        };
        this.#channels = new Map();
        this._broadcaster = new Subject<ChannelEvent>();
        this._connectionState = new BehaviorSubject<ConnectionState>(ConnectionState.DISCONNECTED);
        this._errorSubject = new Subject<Error>();
        this.#init();
    }

    protected abstract _resolveAddress (endpoint: string, params: Record<string, any>): URL;

    public abstract connect (): void;

    protected _clearConnectionTimeout () {
        if (this._connectionTimeoutId) {
            clearTimeout(this._connectionTimeoutId);
            this._connectionTimeoutId = undefined;
        }
    }

    protected _scheduleReconnect () {
        if (this._disconnecting) {
            return;
        }

        const delay = Math.min(
            1000 * 2 ** this._reconnectAttempts,
            this._options.maxReconnectDelay,
        );

        this._reconnectAttempts++;

        setTimeout(() => {
            this.connect();
        }, delay);
    }

    public getState () {
        return this._connectionState.value;
    }

    public abstract disconnect (): void;

    public createChannel (name: string, params?: JoinParams) {
        const channel = this.#channels.get(name);

        if (channel && channel.channelState !== ChannelState.CLOSED) {
            return channel;
        }

        const publisher = this._createPublisher();
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

    protected abstract _createPublisher (): (message: ClientMessage) => void;

    protected _parseMessages (data: string): ChannelEvent[] {
        const events: ChannelEvent[] = [];
        const lines = data.trim().split('\n');

        for (const line of lines) {
            if (line.trim()) {
                const parsed = JSON.parse(line);
                const event = channelEventSchema.parse(parsed);

                events.push(event);
            }
        }

        return events;
    }

    protected _clearChannels () {
        this.#channels.clear();
    }

    #handleAcknowledge (message: ChannelEvent) {
        const channel = this.#channels.get(message.channelName);

        if (!channel) {
            return;
        }

        channel.acknowledge(this._broadcaster);
    }

    #handleUnauthorized (message: ChannelEvent) {
        const channel = this.#channels.get(message.channelName);

        if (channel) {
            const payload = message.payload as { message?: string, statusCode?: number };

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
