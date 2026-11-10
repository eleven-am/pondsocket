import {
    BehaviorSubject,
    buildPondRoute,
    ChannelEvent,
    ChannelState,
    ClientActions,
    AnyPondSchema,
    EventWithResponse,
    EventPayload,
    EventParams,
    ErrorTypes,
    EventsOf,
    JoinParamsOf,
    Params,
    PayloadForResponse,
    PondMessage,
    PondErrorPayload,
    PresenceOf,
    PresenceEventTypes,
    PresencePayload,
    parsePondRoute,
    ResponseForEvent,
    RouteParamsArguments,
    ServerActions,
    Subject,
    Unsubscribe,
    uuid,
} from '@eleven-am/pondsocket-common';

import { ClientMessage, ConnectionState, Publisher } from '../types';

type QueuedMessage = {
    message: ClientMessage;
    onPublished?: () => void;
    onDropped?: (error: Error) => void;
};

const clonePresence = <T>(value: T): T => {
    if (Array.isArray(value)) {
        return value.map((item) => clonePresence(item)) as T;
    }

    if (value && typeof value === 'object') {
        return Object.fromEntries(
            Object.entries(value as Record<string, unknown>)
                .map(([key, item]) => [key, clonePresence(item)]),
        ) as T;
    }

    return value;
};

export class Channel<Schema extends AnyPondSchema = AnyPondSchema> {
    readonly #name: string;

    #queue: QueuedMessage[];

    #presence: PresenceOf<Schema>[];

    #presenceSub: Unsubscribe;

    readonly #publisher: Publisher;

    readonly #joinParams: JoinParamsOf<Schema>;

    readonly #receiver: Subject<ChannelEvent>;

    readonly #clientState: BehaviorSubject<ConnectionState>;

    readonly #joinState: BehaviorSubject<ChannelState>;

    readonly #maxQueueSize: number;

    readonly #joinTimeoutMs: number;

    readonly #errorSubject: Subject<PondErrorPayload>;

    #joinTimeoutId: ReturnType<typeof setTimeout> | undefined;

    #pendingJoinRequestId: string | null;

    #lastError: PondErrorPayload | null;

    #connectionWaitSub: Unsubscribe | null;

    readonly #pendingResponseCleanups: Set<(error?: Error) => void>;

    constructor (
        publisher: Publisher,
        clientState: BehaviorSubject<ConnectionState>,
        name: string,
        params: JoinParamsOf<Schema>,
        maxQueueSize: number = 100,
        joinTimeoutMs: number = 10000,
    ) {
        this.#name = name;
        this.#queue = [];
        this.#presence = [];
        this.#joinParams = params;
        this.#publisher = publisher;
        this.#maxQueueSize = maxQueueSize;
        this.#joinTimeoutMs = joinTimeoutMs;
        this.#clientState = clientState;
        this.#receiver = new Subject<ChannelEvent>();
        this.#errorSubject = new Subject<PondErrorPayload>();
        this.#joinState = new BehaviorSubject<ChannelState>(ChannelState.IDLE);
        this.#joinTimeoutId = undefined;
        this.#pendingJoinRequestId = null;
        this.#lastError = null;
        this.#connectionWaitSub = null;
        this.#pendingResponseCleanups = new Set();
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
    public get presence (): PresenceOf<Schema>[] {
        return clonePresence(this.#presence);
    }

    public get joinError (): PondErrorPayload | null {
        return this.#lastError;
    }

    /**
     * @desc Acknowledges the channel has been joined on the server.
     * @param receiver - The receiver to subscribe to.
     */
    public acknowledge (receiver: Subject<ChannelEvent>, requestId?: string) {
        if (![ChannelState.JOINING, ChannelState.STALLED].includes(this.#joinState.value as ChannelState)) {
            return;
        }

        if (requestId && this.#pendingJoinRequestId && requestId !== this.#pendingJoinRequestId) {
            return;
        }

        this.#clearJoinTimeout();
        this.#connectionWaitSub?.();
        this.#connectionWaitSub = null;
        this.#pendingJoinRequestId = null;
        this.#lastError = null;
        this.#joinState.publish(ChannelState.JOINED);
        this.#init(receiver);
        this.#emptyQueue();
    }

    /**
     * @desc Marks the channel join as declined by the server.
     * @param payload - The decline payload containing status code and message.
     */
    public decline (payload: PondErrorPayload, requestId?: string) {
        if (![ChannelState.JOINING, ChannelState.STALLED].includes(this.#joinState.value as ChannelState)) {
            this.#errorSubject.publish(payload);

            return;
        }

        if (requestId && this.#pendingJoinRequestId && requestId !== this.#pendingJoinRequestId) {
            return;
        }

        this.#clearJoinTimeout();
        this.#connectionWaitSub?.();
        this.#connectionWaitSub = null;
        this.#pendingJoinRequestId = null;
        this.#lastError = payload;
        this.#joinState.publish(ChannelState.DECLINED);
        this.#dropQueue(new Error(`Channel "${this.#name}" join was declined`));
        this.#errorSubject.publish(payload);
    }

    /**
     * @desc Connects to the channel.
     */
    public join () {
        if ([ChannelState.JOINED, ChannelState.JOINING].includes(this.#joinState.value as ChannelState)) {
            return;
        }

        this.#lastError = null;
        this.#joinState.publish(ChannelState.JOINING);
        if (this.#clientState.value === ConnectionState.CONNECTED) {
            this.#sendJoinRequest();
        } else {
            this.#waitForConnection(false);
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

        if (this.#joinState.value === ChannelState.JOINED) {
            this.#publisher(message);
        }

        this.#clearJoinTimeout();
        this.#connectionWaitSub?.();
        this.#connectionWaitSub = null;
        const closedError = new Error(`Channel "${this.#name}" was closed`);

        [...this.#pendingResponseCleanups].forEach((cleanup) => cleanup(closedError));
        this.#pendingResponseCleanups.clear();
        this.#pendingJoinRequestId = null;
        this.#dropQueue(closedError);
        this.#joinState.publish(ChannelState.CLOSED);
        this.#presenceSub();
        this.#receiver.close();
        this.#errorSubject.close();
        this.#joinState.close();
    }

    /**
     * @desc Monitors the channel state of the channel.
     * @param callback - The callback to call when the connection state changes.
     */
    public onChannelStateChange (callback: (connected: ChannelState) => void): Unsubscribe {
        return this.#joinState.subscribe((data) => {
            callback(data);
        });
    }

    public onError (callback: (error: PondErrorPayload) => void): Unsubscribe {
        return this.#errorSubject.subscribe(callback);
    }

    /**
     * @desc Detects when clients join the channel.
     * @param callback - The callback to call when a client joins the channel.
     */
    public onJoin (callback: (presence: PresenceOf<Schema>) => void): Unsubscribe {
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
    public onLeave (callback: (presence: PresenceOf<Schema>) => void): Unsubscribe {
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
    public onMessage<Event extends Extract<keyof EventsOf<Schema>, string>> (callback: (event: Event, message: EventPayload<EventsOf<Schema>, Event>) => void): Unsubscribe {
        return this.#onMessage((event, message) => {
            callback(event as Event, message as EventPayload<EventsOf<Schema>, Event>);
        });
    }

    /**
     * @desc Monitors the channel for messages.
     * @param event - The event to monitor.
     * @param callback - The callback to call when a message is received.
     */
    public onMessageEvent<Event extends Extract<keyof EventsOf<Schema>, string>> (
        event: Event,
        callback: (message: EventPayload<EventsOf<Schema>, Event>, eventParams: EventParams<Event>) => void,
    ): Unsubscribe {
        return this.onMessage((eventReceived, message) => {
            const params = parsePondRoute(event, eventReceived);

            if (params) {
                return callback(message as EventPayload<EventsOf<Schema>, Event>, params);
            }
        });
    }

    /**
     * @desc Detects when clients change their presence in the channel.
     * @param callback - The callback to call when a client changes their presence in the channel.
     */
    public onPresenceChange (callback: (presence: PresencePayload<PresenceOf<Schema>>) => void): Unsubscribe {
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
    public onUsersChange (callback: (users: PresenceOf<Schema>[]) => void): Unsubscribe {
        return this.#subscribeToPresence((_event, payload) => callback(payload.presence));
    }

    /**
     * @desc Sends a message to specific clients in the channel.
     * @param event - The event to send.
     * @param payload - The message to send.
     */
    public sendMessage<Event extends Extract<keyof EventsOf<Schema>, string>> (
        event: Event,
        payload: EventPayload<EventsOf<Schema>, Event>,
        ...args: RouteParamsArguments<Event>
    ) {
        const requestId = uuid();

        const message: ClientMessage = {
            action: ClientActions.BROADCAST,
            channelName: this.#name,
            requestId,
            event: buildPondRoute(event, args[0] ?? {} as Params<Event>),
            payload,
        };

        this.#publish(message);
    }

    public sendForResponse<Event extends EventWithResponse<EventsOf<Schema>> & string> (
        sentEvent: Event,
        payload: PayloadForResponse<EventsOf<Schema>, Event>,
        ...args: keyof Params<Event> extends never
            ? [timeoutMs?: number]
            : [params: Params<Event>, timeoutMs?: number]
    ) {
        const hasParams = typeof args[0] === 'object';
        const params = hasParams ? args[0] as Params<Event> : {} as Params<Event>;
        const timeoutMs = (hasParams ? args[1] : args[0]) as number | undefined ?? 30000;
        const requestId = uuid();

        return new Promise<ResponseForEvent<EventsOf<Schema>, Event>>((resolve, reject) => {
            let timer: ReturnType<typeof setTimeout> | undefined;
            let settled = false;
            let unsubscribe: Unsubscribe = () => {};
            const finish = (error?: Error) => {
                if (settled) {
                    return false;
                }

                settled = true;
                if (timer) {
                    clearTimeout(timer);
                    timer = undefined;
                }
                unsubscribe();
                this.#removeQueuedMessage(requestId);
                this.#pendingResponseCleanups.delete(finish);
                if (error) {
                    reject(error);
                }

                return true;
            };

            unsubscribe = this.#onMessage((_, message, responseId) => {
                if (requestId === responseId && finish()) {
                    resolve(message as ResponseForEvent<EventsOf<Schema>, Event>);
                }
            });

            this.#pendingResponseCleanups.add(finish);
            timer = setTimeout(() => {
                finish(new Error(`sendForResponse timed out after ${timeoutMs}ms for event "${String(sentEvent)}"`));
            }, timeoutMs);
            (timer as ReturnType<typeof setTimeout> & { unref?: () => void }).unref?.();

            const message: ClientMessage = {
                action: ClientActions.BROADCAST,
                channelName: this.#name,
                requestId,
                event: buildPondRoute(sentEvent, params),
                payload,
            };

            try {
                this.#publish(message, {
                    onDropped: (error) => finish(error),
                });
            } catch (error) {
                finish(error instanceof Error ? error : new Error(String(error)));
            }
        });
    }

    /**
     * @desc Clears messages from the queue and publishes them to the server.
     * @private
     */
    #emptyQueue () {
        while (this.#queue.length > 0) {
            const entry = this.#queue[0];

            try {
                this.#dispatch(entry);
                this.#queue.shift();
            } catch (error) {
                this.#joinState.publish(ChannelState.STALLED);
                return error instanceof Error ? error : new Error(String(error));
            }
        }

        return null;
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
            if (state === ConnectionState.CONNECTED && this.#joinState.value === ChannelState.STALLED) {
                this.#sendJoinRequest();
            } else if (state !== ConnectionState.CONNECTED && this.#joinState.value === ChannelState.JOINED) {
                this.#clearJoinTimeout();
                this.#pendingJoinRequestId = null;
                this.#joinState.publish(ChannelState.STALLED);
            }
        });

        const unsubPresence = this.#subscribeToPresence((_, payload) => {
            this.#presence = clonePresence(payload.presence);
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
    #publish (data: ClientMessage, hooks: Omit<QueuedMessage, 'message'> = {}) {
        const entry: QueuedMessage = {
            message: data,
            ...hooks,
        };

        if ([ChannelState.DECLINED, ChannelState.CLOSED].includes(this.#joinState.value as ChannelState)) {
            throw new Error(`Cannot send messages while channel "${this.#name}" is ${this.#joinState.value}`);
        }

        if (this.#queue.length >= this.#maxQueueSize) {
            const error = new Error(`Channel "${this.#name}" message queue capacity was exceeded`);

            entry.onDropped?.(error);
            this.#errorSubject.publish({
                code: ErrorTypes.CHANNEL_ERROR,
                message: error.message,
                status: 429,
                statusCode: 429,
            });

            throw error;
        }

        this.#queue.push(entry);
        if (this.#joinState.value === ChannelState.JOINED) {
            const error = this.#emptyQueue();

            if (error) {
                throw error;
            }
        }
    }

    #dispatch (entry: QueuedMessage) {
        try {
            this.#publisher(entry.message);
            entry.onPublished?.();
        } catch (error) {
            const failure = error instanceof Error ? error : new Error(String(error));

            this.#errorSubject.publish({
                code: ErrorTypes.CHANNEL_ERROR,
                message: failure.message,
                status: 503,
                statusCode: 503,
            });
            throw failure;
        }
    }

    #dropQueue (error: Error) {
        const queue = this.#queue;

        this.#queue = [];
        queue.forEach((entry) => entry.onDropped?.(error));
    }

    #removeQueuedMessage (requestId: string) {
        this.#queue = this.#queue.filter((entry) => entry.message.requestId !== requestId);
    }

    #sendJoinRequest () {
        if (this.#pendingJoinRequestId) {
            return;
        }
        const requestId = uuid();
        const message: ClientMessage = {
            action: ClientActions.JOIN_CHANNEL,
            event: ClientActions.JOIN_CHANNEL,
            payload: this.#joinParams,
            channelName: this.#name,
            requestId,
        };

        this.#clearJoinTimeout();
        this.#pendingJoinRequestId = requestId;
        try {
            this.#publisher(message);
        } catch (error) {
            if (this.#pendingJoinRequestId === requestId) {
                this.#pendingJoinRequestId = null;
            }
            this.#joinState.publish(ChannelState.STALLED);
            const failure = error instanceof Error ? error : new Error(String(error));

            this.#errorSubject.publish({
                code: ErrorTypes.CHANNEL_ERROR,
                message: failure.message,
                status: 503,
                statusCode: 503,
            });
            this.#waitForConnection(true);

            return;
        }
        this.#watchJoinAttempt(requestId);
        this.#joinTimeoutId = setTimeout(() => {
            if (this.#pendingJoinRequestId !== requestId) {
                return;
            }

            this.decline({
                code: ErrorTypes.CHANNEL_ERROR,
                message: `Timed out joining channel "${this.#name}"`,
                status: 408,
                statusCode: 408,
            }, requestId);
        }, this.#joinTimeoutMs);
        (this.#joinTimeoutId as ReturnType<typeof setTimeout> & { unref?: () => void }).unref?.();
    }

    #waitForConnection (requireDisconnect: boolean) {
        this.#connectionWaitSub?.();
        let observedDisconnect = !requireDisconnect && this.#clientState.value !== ConnectionState.CONNECTED;
        let unsubscribe: Unsubscribe = () => {};

        unsubscribe = this.#clientState.subscribe((state) => {
            if (state !== ConnectionState.CONNECTED) {
                observedDisconnect = true;

                return;
            }
            if (!observedDisconnect) {
                return;
            }

            unsubscribe();
            this.#connectionWaitSub = null;
            if ([ChannelState.JOINING, ChannelState.STALLED].includes(this.#joinState.value as ChannelState)) {
                this.#sendJoinRequest();
            }
        });
        this.#connectionWaitSub = unsubscribe;
    }

    #watchJoinAttempt (requestId: string) {
        this.#connectionWaitSub?.();
        let unsubscribe: Unsubscribe = () => {};

        unsubscribe = this.#clientState.subscribe((state) => {
            if (state === ConnectionState.CONNECTED || this.#pendingJoinRequestId !== requestId) {
                return;
            }

            unsubscribe();
            this.#connectionWaitSub = null;
            this.#clearJoinTimeout();
            this.#pendingJoinRequestId = null;
            if ([ChannelState.JOINING, ChannelState.STALLED].includes(this.#joinState.value as ChannelState)) {
                this.#joinState.publish(ChannelState.STALLED);
                this.#waitForConnection(false);
            }
        });
        this.#connectionWaitSub = unsubscribe;
    }

    #clearJoinTimeout () {
        if (this.#joinTimeoutId) {
            clearTimeout(this.#joinTimeoutId);
            this.#joinTimeoutId = undefined;
        }
    }

    /**
     * @desc Publishes all presence events to the channel.
     * @param callback - The callback to call when a presence event is received.
     * @private
     */
    #subscribeToPresence (callback: (event: PresenceEventTypes, payload: PresencePayload) => void) {
        return this.#receiver.subscribe((data) => {
            if (data.action === ServerActions.PRESENCE) {
                return callback(data.event, clonePresence(data.payload));
            }
        });
    }
}
