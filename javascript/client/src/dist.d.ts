import {
    ChannelState,
    EventWithResponse,
    JoinParams,
    PayloadForResponse,
    PondEventMap,
    PondPresence,
    PresencePayload,
    ResponseForEvent,
    Unsubscribe,
} from '@eleven-am/pondsocket-common';

declare class Channel<EventMap extends PondEventMap = PondEventMap, Presence extends PondPresence = PondPresence> {
    /**
     * @desc The current connection state of the channel.
     */
    channelState: ChannelState;

    /**
     * @desc Gets the current presence of the channel.
     */
    presence (): Presence[];

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
    onJoin (callback: (presence: Presence) => void): Unsubscribe;

    /**
     * @desc Detects when clients leave the channel.
     * @param callback - The callback to call when a client leaves the channel.
     */
    onLeave (callback: (presence: Presence) => void): Unsubscribe;

    /**
     * @desc Monitors the channel for messages.
     * @param callback - The callback to call when a message is received.
     */
    onMessage<Event extends keyof EventMap> (callback: (event: Event, message: EventMap[Event]) => void): Unsubscribe;

    /**
     * @desc Monitors the channel for messages.
     * @param event - The event to monitor.
     * @param callback - The callback to call when a message is received.
     */
    onMessageEvent<Event extends keyof EventMap> (event: Event, callback: (message: EventMap[Event]) => void): Unsubscribe;

    /**
     * @desc Detects when clients change their presence in the channel.
     * @param callback - The callback to call when a client changes their presence in the channel.
     */
    onPresenceChange (callback: (presence: PresencePayload<Presence>) => void): Unsubscribe;

    /**
     * @desc Monitors the presence of the channel.
     * @param callback - The callback to call when the presence changes.
     */
    onUsersChange (callback: (users: Presence[]) => void): Unsubscribe;

    /**
     * @desc Sends a message to specific clients in the channel.
     * @param event - The event to send.
     * @param payload - The message to send.
     */
    sendMessage<Event extends keyof EventMap> (event: Event, payload: EventMap[Event]): void;

    /**
     * @desc Sends a message to the server and waits for a response.
     * @param sentEvent - The event to send.
     * @param payload - The message to send.
     */
    sendForResponse<Event extends EventWithResponse<EventMap>> (sentEvent: Event, payload: PayloadForResponse<EventMap, Event>): Promise<ResponseForEvent<EventMap, Event>>;
}

declare class PondClient {
    constructor (endpoint: string, params?: Record<string, any>);

    /**
     * @desc Connects to the server and returns the socket.
     */
    connect (): void;

    /**
     * @desc Returns the current state of the socket.
     */
    getState (): boolean;

    /**
     * @desc Disconnects the socket.
     */
    disconnect (): void;

    /**
     * @desc Creates a channel with the given name and params.
     * @param name - The name of the channel.
     * @param params - The params to send to the server.
     */
    createChannel<EventType extends PondEventMap = PondEventMap, Presence extends PondPresence = PondPresence> (name: string, params?: JoinParams): Channel<EventType, Presence>;

    /**
     * @desc Subscribes to the connection state.
     * @param callback - The callback to call when the state changes.
     */
    onConnectionChange (callback: (state: boolean) => void): Unsubscribe;
}
