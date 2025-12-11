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
    PondMessage,
} from '@eleven-am/pondsocket-common';

import { Channel } from '../core/channel';
import { ClientMessage, ClientOptions, ConnectionState } from '../types';

const DEFAULT_CONNECTION_TIMEOUT = 10000;
const DEFAULT_MAX_RECONNECT_DELAY = 30000;

export class PondClient {
    protected readonly _address: URL;

    protected readonly _options: Required<Omit<ClientOptions, 'pingInterval'>> & Pick<ClientOptions, 'pingInterval'>;

    protected _disconnecting: boolean;

    protected _socket: WebSocket | any | undefined;

    protected _reconnectAttempts: number;

    protected _connectionTimeoutId: ReturnType<typeof setTimeout> | undefined;

    protected _pingIntervalId: ReturnType<typeof setInterval> | undefined;

    protected readonly _broadcaster: Subject<ChannelEvent>;

    protected readonly _connectionState: BehaviorSubject<ConnectionState>;

    protected readonly _errorSubject: Subject<Error>;

    #channels: Map<string, Channel>;

    constructor (endpoint: string, params: Record<string, any> = {}, options: ClientOptions = {}) {
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
        const protocol = address.protocol === 'https:' ? 'wss:' : 'ws:';

        if (address.protocol !== 'wss:' && address.protocol !== 'ws:') {
            address.protocol = protocol;
        }

        this._address = address;
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

    /**
     * @desc Connects to the server and returns the socket.
     */
    public connect () {
        this._disconnecting = false;
        this._connectionState.publish(ConnectionState.CONNECTING);

        const socket = new WebSocket(this._address.toString());

        this._connectionTimeoutId = setTimeout(() => {
            if (socket.readyState === WebSocket.CONNECTING) {
                const error = new Error('Connection timeout');

                this._errorSubject.publish(error);
                socket.close();
            }
        }, this._options.connectionTimeout);

        socket.onopen = () => {
            this.#clearTimeouts();
            this._reconnectAttempts = 0;

            if (this._options.pingInterval) {
                this._pingIntervalId = setInterval(() => {
                    if (socket.readyState === WebSocket.OPEN) {
                        socket.send(JSON.stringify({ action: 'ping' }));
                    }
                }, this._options.pingInterval);
            }
        };

        socket.onmessage = (message) => {
            const lines = (message.data as string).trim().split('\n');

            for (const line of lines) {
                if (line.trim()) {
                    const data = JSON.parse(line);
                    const event = channelEventSchema.parse(data);

                    this._broadcaster.publish(event);
                }
            }
        };

        socket.onerror = (event) => {
            const error = new Error('WebSocket error');

            this._errorSubject.publish(error);
            socket.close();
        };

        socket.onclose = () => {
            this.#clearTimeouts();
            this._connectionState.publish(ConnectionState.DISCONNECTED);

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
        };

        this._socket = socket;
    }

    #clearTimeouts () {
        if (this._connectionTimeoutId) {
            clearTimeout(this._connectionTimeoutId);
            this._connectionTimeoutId = undefined;
        }

        if (this._pingIntervalId) {
            clearInterval(this._pingIntervalId);
            this._pingIntervalId = undefined;
        }
    }

    /**
     * @desc Returns the current state of the socket.
     */
    public getState () {
        return this._connectionState.value;
    }

    /**
     * @desc Disconnects the socket.
     */
    public disconnect () {
        this.#clearTimeouts();
        this._disconnecting = true;
        this._connectionState.publish(ConnectionState.DISCONNECTED);
        this._socket?.close();
        this.#channels.clear();
    }

    /**
     * @desc Creates a channel with the given name and params.
     * @param name - The name of the channel.
     * @param params - The params to send to the server.
     */
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

    /**
     * @desc Subscribes to the connection state.
     * @param callback - The callback to call when the state changes.
     */
    public onConnectionChange (callback: (state: ConnectionState) => void) {
        return this._connectionState.subscribe(callback);
    }

    /**
     * @desc Subscribes to connection errors.
     * @param callback - The callback to call when an error occurs.
     */
    public onError (callback: (error: Error) => void): Unsubscribe {
        return this._errorSubject.subscribe(callback);
    }

    /**
     * @desc Returns a function that publishes a message to the socket.
     * @private
     */
    #createPublisher () {
        return (message: ClientMessage) => {
            if (this._connectionState.value === ConnectionState.CONNECTED) {
                this._socket.send(JSON.stringify(message));
            }
        };
    }

    /**
     * @desc Handles an acknowledge event. this event is sent when the server adds a client to a channel.
     * @param message - The message to handle.
     * @private
     */
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

    /**
     * @desc Handles an unauthorized event. This event is sent when the server declines a channel join.
     * @param message - The message to handle.
     * @private
     */
    #handleUnauthorized (message: ChannelEvent) {
        const channel = this.#channels.get(message.channelName);

        if (channel) {
            const payload = message.payload as { message?: string; statusCode?: number };

            channel.decline(payload);
        }
    }

    /**
     * @desc Initializes the client.
     * @private
     */
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
