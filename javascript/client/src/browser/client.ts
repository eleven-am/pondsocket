import {
    BehaviorSubject,
    ChannelEvent,
    Events,
    JoinParams,
    ServerActions,
    Subject,
    channelEventSchema,
    ChannelState,
} from '@eleven-am/pondsocket-common';

import { Channel } from '../core/channel';
import { ClientMessage } from '../types';

export default class PondClient {
    protected readonly _address: URL;

    protected _disconnecting: boolean;

    protected _socket: WebSocket | any | undefined;

    protected readonly _broadcaster: Subject<ChannelEvent>;

    protected readonly _connectionState: BehaviorSubject<boolean>;

    #channels: Map<string, Channel>;

    constructor (endpoint: string, params: Record<string, any> = {}) {
        let address: URL;

        try {
            address = new URL(endpoint);
        } catch (e) {
            address = new URL(window.location.toString());
            address.pathname = endpoint;
        }

        this._disconnecting = false;
        const query = new URLSearchParams(params);

        address.search = query.toString();
        const protocol = address.protocol === 'https:' ? 'wss:' : 'ws:';

        if (address.protocol !== 'wss:' && address.protocol !== 'ws:') {
            address.protocol = protocol;
        }

        this._address = address;
        this.#channels = new Map();

        this._broadcaster = new Subject<ChannelEvent>();
        this._connectionState = new BehaviorSubject<boolean>(false);
        this.#init();
    }

    /**
     * @desc Connects to the server and returns the socket.
     */
    public connect () {
        this._disconnecting = false;

        const socket = new WebSocket(this._address.toString());

        socket.onmessage = (message) => {
            const data = JSON.parse(message.data);

            const event = channelEventSchema.parse(data);

            this._broadcaster.publish(event);
        };

        socket.onerror = () => socket.close();

        socket.onclose = () => {
            this._connectionState.publish(false);
            if (this._disconnecting) {
                return;
            }

            setTimeout(() => {
                this.connect();
            }, 1000);
        };

        this._socket = socket;
    }

    /**
     * @desc Returns the current state of the socket.
     */
    public getState () {
        return this._connectionState.value as boolean;
    }

    /**
     * @desc Disconnects the socket.
     */
    public disconnect () {
        this._connectionState.publish(false);
        this._disconnecting = true;
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
    public onConnectionChange (callback: (state: boolean) => void) {
        return this._connectionState.subscribe(callback);
    }

    /**
     * @desc Returns a function that publishes a message to the socket.
     * @private
     */
    #createPublisher () {
        return (message: ClientMessage) => {
            if (this._connectionState.value) {
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
     * @desc Initializes the client.
     * @private
     */
    #init () {
        this._broadcaster.subscribe((message) => {
            if (message.event === Events.ACKNOWLEDGE) {
                this.#handleAcknowledge(message);
            } else if (message.event === Events.CONNECTION && message.action === ServerActions.CONNECT) {
                this._connectionState.publish(true);
            }
        });
    }
}
