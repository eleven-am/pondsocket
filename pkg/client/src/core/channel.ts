import {
    BehaviorSubject,
    ChannelEvent,
    ChannelState,
    ClientActions,
    EventWithResponse,
    JoinParams,
    PayloadForResponse,
    PondEventMap,
    PondMessage,
    PondPresence,
    PresenceEventTypes,
    PresencePayload,
    ResponseForEvent,
    ServerActions,
    Subject,
    Unsubscribe,
    uuid,
} from '@eleven-am/pondsocket-common';

import { ClientMessage, Publisher } from '../types';

export class Channel<EventMap extends PondEventMap = PondEventMap> {
    readonly #name: string;

    #queue: ClientMessage[];

    #presence: PondPresence[];

    #presenceSub: Unsubscribe;

    readonly #publisher: Publisher;

    readonly #joinParams: JoinParams;

    readonly #receiver: Subject<ChannelEvent>;

    readonly #clientState: BehaviorSubject<boolean>;

    readonly #joinState: BehaviorSubject<ChannelState>;

    constructor (publisher: Publisher, clientState: BehaviorSubject<boolean>, name: string, params: JoinParams) {
        this.#name = name;
        this.#queue = [];
        this.#presence = [];
        this.#joinParams = params;
        this.#publisher = publisher;
        this.#clientState = clientState;
        this.#receiver = new Subject<ChannelEvent>();
        this.#joinState = new BehaviorSubject<ChannelState>(ChannelState.IDLE);
        this.#presenceSub = () => {
            // do nothing
        };
    }

    /**
     * @desc Gets the current connection state of the channel.
     */
    public get channelState (): ChannelState {
        return this.#joinState.value as ChannelState;
    }

    /**
     * @desc Gets the current presence of the channel.
     */
    public get presence (): PondPresence[] {
        return this.#presence;
    }

    /**
     * @desc Acknowledges the channel has been joined on the server.
     * @param receiver - The receiver to subscribe to.
     */
    public acknowledge (receiver: Subject<ChannelEvent>) {
        this.#joinState.publish(ChannelState.JOINED);
        this.#init(receiver);
        this.#emptyQueue();
    }

    /**
     * @desc Connects to the channel.
     */
    public join () {
        const message: ClientMessage = {
            action: ClientActions.JOIN_CHANNEL,
            event: ClientActions.JOIN_CHANNEL,
            payload: this.#joinParams,
            channelName: this.#name,
            requestId: uuid(),
        };

        if (this.#joinState.value === ChannelState.JOINED) {
            return;
        }

        this.#joinState.publish(ChannelState.JOINING);
        if (this.#clientState.value) {
            this.#publisher(message);
        } else {
            const unsubscribe = this.#clientState.subscribe((state) => {
                if (state) {
                    unsubscribe();

                    if (this.#joinState.value === ChannelState.JOINING) {
                        this.#publisher(message);
                    }
                }
            });
        }
    }

    /**
     * @desc Disconnects from the channel.
     */
    public leave () {
        const message: ClientMessage = {
            action: ClientActions.LEAVE_CHANNEL,
            event: ClientActions.LEAVE_CHANNEL,
            channelName: this.#name,
            requestId: uuid(),
            payload: {},
        };

        this.#publish(message);
        this.#joinState.publish(ChannelState.CLOSED);
        this.#presenceSub();
    }

    /**
     * @desc Monitors the channel state of the channel.
     * @param callback - The callback to call when the connection state changes.
     */
    public onChannelStateChange (callback: (connected: ChannelState) => void) {
        return this.#joinState.subscribe((data) => {
            callback(data);
        });
    }

    /**
     * @desc Detects when clients join the channel.
     * @param callback - The callback to call when a client joins the channel.
     */
    public onJoin (callback: (presence: PondPresence) => void) {
        return this.#subscribeToPresence((event, payload) => {
            if (event === PresenceEventTypes.JOIN) {
                return callback(payload.changed);
            }
        });
    }

    /**
     * @desc Detects when clients leave the channel.
     * @param callback - The callback to call when a client leaves the channel.
     */
    public onLeave (callback: (presence: PondPresence) => void) {
        return this.#subscribeToPresence((event, payload) => {
            if (event === PresenceEventTypes.LEAVE) {
                return callback(payload.changed);
            }
        });
    }

    /**
     * @desc Monitors the channel for messages.
     * @param callback - The callback to call when a message is received.
     */
    public onMessage<Event extends keyof EventMap> (callback: (event: Event, message: EventMap[Event]) => void) {
        return this.#onMessage((event, message) => {
            callback(event as Event, message as EventMap[Event]);
        });
    }

    /**
     * @desc Monitors the channel for messages.
     * @param event - The event to monitor.
     * @param callback - The callback to call when a message is received.
     */
    public onMessageEvent<Event extends keyof EventMap> (event: Event, callback: (message: EventMap[Event]) => void) {
        return this.onMessage((eventReceived, message) => {
            if (eventReceived === event) {
                return callback(message as EventMap[Event]);
            }
        });
    }

    /**
     * @desc Detects when clients change their presence in the channel.
     * @param callback - The callback to call when a client changes their presence in the channel.
     */
    public onPresenceChange (callback: (presence: PresencePayload) => void) {
        return this.#subscribeToPresence((event, payload) => {
            if (event === PresenceEventTypes.UPDATE) {
                return callback(payload);
            }
        });
    }

    /**
     * @desc Monitors the presence of the channel.
     * @param callback - The callback to call when the presence changes.
     */
    public onUsersChange (callback: (users: PondPresence[]) => void) {
        return this.#subscribeToPresence((_event, payload) => callback(payload.presence));
    }

    /**
     * @desc Sends a message to specific clients in the channel.
     * @param event - The event to send.
     * @param payload - The message to send.
     */
    public sendMessage (event: string, payload: PondMessage) {
        const requestId = uuid();

        const message: ClientMessage = {
            action: ClientActions.BROADCAST,
            channelName: this.#name,
            requestId,
            event,
            payload,
        };

        this.#publish(message);
    }

    /**
     * @desc Sends a message to the server and waits for a response.
     * @param sentEvent - The event to send.
     * @param payload - The message to send.
     */
    public sendForResponse<Event extends EventWithResponse<EventMap>> (sentEvent: Event, payload: PayloadForResponse<EventMap, Event>) {
        const requestId = uuid();

        return new Promise<ResponseForEvent<EventMap, Event>>((resolve) => {
            const unsub = this.#onMessage((_, message, responseId) => {
                if (requestId === responseId) {
                    resolve(message as ResponseForEvent<EventMap, Event>);
                    unsub();
                }
            });

            const message: ClientMessage = {
                action: ClientActions.BROADCAST,
                channelName: this.#name,
                requestId,
                event: sentEvent as string,
                payload,
            };

            this.#publish(message);
        });
    }

    /**
     * @desc Clears messages from the queue and publishes them to the server.
     * @private
     */
    #emptyQueue () {
        this.#queue
            .forEach((message) => this.#publisher(message));

        this.#queue = [];
    }

    /**
     * @desc Initializes the channel.
     * @param receiver - The receiver to subscribe to.
     * @private
     */
    #init (receiver: Subject<ChannelEvent>) {
        this.#presenceSub();
        const unsubMessages = receiver.subscribe((data) => {
            if (data.channelName === this.#name && this.channelState === ChannelState.JOINED) {
                this.#receiver.publish(data);
            }
        });

        const unsubStateChange = this.#clientState.subscribe((state) => {
            if (state && this.#joinState.value === ChannelState.STALLED) {
                const message: ClientMessage = {
                    action: ClientActions.JOIN_CHANNEL,
                    event: ClientActions.JOIN_CHANNEL,
                    payload: this.#joinParams,
                    channelName: this.#name,
                    requestId: uuid(),
                };

                this.#publisher(message);
            } else if (!state && this.#joinState.value === ChannelState.JOINED) {
                this.#joinState.publish(ChannelState.STALLED);
            }
        });

        const unsubPresence = this.#subscribeToPresence((_, payload) => {
            this.#presence = payload.presence;
        });

        this.#presenceSub = () => {
            unsubMessages();
            unsubStateChange();
            unsubPresence();
        };
    }

    /**
     * @desc Monitors the channel for messages.
     * @param callback
     * @private
     */
    #onMessage (callback: (event: string, message: PondMessage, requestId: string) => void) {
        return this.#receiver.subscribe((data) => {
            if (data.action !== ServerActions.PRESENCE) {
                return callback(data.event, data.payload, data.requestId);
            }
        });
    }

    /**
     * @desc Publishes a message received from the server.
     * @param data - The message to publish.
     * @private
     */
    #publish (data: ClientMessage) {
        if (this.#joinState.value === ChannelState.JOINED) {
            return this.#publisher(data);
        }

        this.#queue.push(data);
    }

    /**
     * @desc Publishes all presence events to the channel.
     * @param callback - The callback to call when a presence event is received.
     * @private
     */
    #subscribeToPresence (callback: (event: PresenceEventTypes, payload: PresencePayload) => void) {
        return this.#receiver.subscribe((data) => {
            if (data.action === ServerActions.PRESENCE) {
                return callback(data.event, data.payload);
            }
        });
    }
}
