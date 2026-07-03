import {
    ChannelState,
    AnyPondSchema,
    EventPayload,
    EventWithResponse,
    EventsOf,
    JoinParamsOf,
    PayloadForResponse,
    PresenceOf,
    PresencePayload,
    ResponseForEvent,
    Unsubscribe,
} from '@eleven-am/pondsocket-common';

declare enum ConnectionState {
    DISCONNECTED = 'DISCONNECTED',
    CONNECTING = 'CONNECTING',
    CONNECTED = 'CONNECTED',
}

interface ClientOptions {
    connectionTimeout?: number;
    maxReconnectDelay?: number;
    pingInterval?: number;
}

interface SSEClientOptions extends ClientOptions {
    withCredentials?: boolean;
}

declare class Channel<Schema extends AnyPondSchema = AnyPondSchema> {
    /**
     * @desc The current connection state of the channel.
     */
    channelState: ChannelState;

    /**
     * @desc Gets the current presence of the channel.
     */
    readonly presence: PresenceOf<Schema>[];

    /**
     * @desc Connects to the channel.
     */
    join (): void;

    /**
     * @desc Disconnects from the channel.
     */
    leave (): void;

    /**
     * @desc Monitors the channel state of the channel.
     * @param callback - The callback to call when the connection state changes.
     */
    onChannelStateChange (callback: (connected: ChannelState) => void): Unsubscribe;

    /**
     * @desc Detects when clients join the channel.
     * @param callback - The callback to call when a client joins the channel.
     */
    onJoin (callback: (presence: PresenceOf<Schema>) => void): Unsubscribe;

    /**
     * @desc Detects when clients leave the channel.
     * @param callback - The callback to call when a client leaves the channel.
     */
    onLeave (callback: (presence: PresenceOf<Schema>) => void): Unsubscribe;

    /**
     * @desc Monitors the channel for messages.
     * @param callback - The callback to call when a message is received.
     */
    onMessage<Event extends Extract<keyof EventsOf<Schema>, string>> (callback: (event: Event, message: EventPayload<EventsOf<Schema>, Event>) => void): Unsubscribe;

    /**
     * @desc Monitors the channel for messages.
     * @param event - The event to monitor.
     * @param callback - The callback to call when a message is received.
     */
    onMessageEvent<Event extends Extract<keyof EventsOf<Schema>, string>> (event: Event, callback: (message: EventPayload<EventsOf<Schema>, Event>) => void): Unsubscribe;

    /**
     * @desc Detects when clients change their presence in the channel.
     * @param callback - The callback to call when a client changes their presence in the channel.
     */
    onPresenceChange (callback: (presence: PresencePayload<PresenceOf<Schema>>) => void): Unsubscribe;

    /**
     * @desc Monitors the presence of the channel.
     * @param callback - The callback to call when the presence changes.
     */
    onUsersChange (callback: (users: PresenceOf<Schema>[]) => void): Unsubscribe;

    /**
     * @desc Sends a message to specific clients in the channel.
     * @param event - The event to send.
     * @param payload - The message to send.
     */
    sendMessage<Event extends Extract<keyof EventsOf<Schema>, string>> (event: Event, payload: EventPayload<EventsOf<Schema>, Event>): void;

    /**
     * @desc Sends a message to the server and waits for a response.
     * @param sentEvent - The event to send.
     * @param payload - The message to send.
     */
    sendForResponse<Event extends EventWithResponse<EventsOf<Schema>>> (sentEvent: Event, payload: PayloadForResponse<EventsOf<Schema>, Event>, timeoutMs?: number): Promise<ResponseForEvent<EventsOf<Schema>, Event>>;
}

declare class PondClient {
    constructor (endpoint: string, params?: Record<string, any>, options?: ClientOptions);

    /**
     * @desc Connects to the server and returns the socket.
     */
    connect (): void;

    /**
     * @desc Returns the current state of the socket.
     */
    getState (): ConnectionState;

    /**
     * @desc Disconnects the socket.
     */
    disconnect (): void;

    /**
     * @desc Creates a channel with the given name and params.
     * @param name - The name of the channel.
     * @param params - The params to send to the server.
     */
    createChannel<Schema extends AnyPondSchema = AnyPondSchema> (name: string, params?: JoinParamsOf<Schema>): Channel<Schema>;

    /**
     * @desc Subscribes to the connection state.
     * @param callback - The callback to call when the state changes.
     */
    onConnectionChange (callback: (state: ConnectionState) => void): Unsubscribe;

    /**
     * @desc Subscribes to error events.
     * @param callback - The callback to call when an error occurs.
     */
    onError (callback: (error: Error) => void): Unsubscribe;
}

declare class SSEClient {
    constructor (endpoint: string, params?: Record<string, any>, options?: SSEClientOptions);

    /**
     * @desc Connects to the server using Server-Sent Events.
     */
    connect (): void;

    /**
     * @desc Returns the current state of the connection.
     */
    getState (): ConnectionState;

    /**
     * @desc Returns the connection ID assigned by the server.
     */
    getConnectionId (): string | undefined;

    /**
     * @desc Disconnects the SSE connection.
     */
    disconnect (): void;

    /**
     * @desc Creates a channel with the given name and params.
     * @param name - The name of the channel.
     * @param params - The params to send to the server.
     */
    createChannel<Schema extends AnyPondSchema = AnyPondSchema> (name: string, params?: JoinParamsOf<Schema>): Channel<Schema>;

    /**
     * @desc Subscribes to the connection state.
     * @param callback - The callback to call when the state changes.
     */
    onConnectionChange (callback: (state: ConnectionState) => void): Unsubscribe;

    /**
     * @desc Subscribes to error events.
     * @param callback - The callback to call when an error occurs.
     */
    onError (callback: (error: Error) => void): Unsubscribe;
}
