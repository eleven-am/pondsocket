import type {
    AnyPondSchema,
    AssignsOf,
    EventPayload,
    EventReplyPayload,
    EventsOf,
    JoinParamsOf,
    Params,
    PondEvent,
    PondMessage,
    PresenceOf,
    ServerActions,
    UserData,
} from '@eleven-am/pondsocket-common';

import { NestContext } from '../types';

export class Context<
    Schema extends AnyPondSchema = AnyPondSchema,
    Path extends string = string,
    EventName extends Extract<keyof EventsOf<Schema>, string> = Extract<keyof EventsOf<Schema>, string>,
> {
    readonly #data: Record<string, unknown> = {};

    readonly #context: NestContext<Schema, Path, EventName>;

    readonly #instance: any;

    readonly #propertyKey: string;

    constructor (
        context: NestContext<Schema, Path, EventName>,
        instance: any,
        propertyKey: string,
    ) {
        this.#context = context;
        this.#instance = instance;
        this.#propertyKey = propertyKey;
    }

    get joinContext () {
        return this.#context.join ?? null;
    }

    get eventContext () {
        return this.#context.event ?? null;
    }

    get outgoingContext () {
        return this.#context.outgoing ?? null;
    }

    get connectionContext () {
        return this.#context.connection ?? null;
    }

    get leaveEvent () {
        return this.#context.leave ?? null;
    }

    get payload (): EventPayload<EventsOf<Schema>, EventName> | EventReplyPayload<EventsOf<Schema>, EventName> | PondMessage | null {
        return this.eventContext?.payload ?? this.outgoingContext?.payload ?? null;
    }

    get action (): ServerActions.BROADCAST | ServerActions.SYSTEM | ServerActions.ERROR | ServerActions.CONNECT | null {
        return this.outgoingContext?.action ?? null;
    }

    get params (): Params<Path> {
        return (this.event?.params ?? {}) as Params<Path>;
    }

    get query (): Record<string, string> {
        return this.event?.query ?? {};
    }

    get joinParams (): JoinParamsOf<Schema> | null {
        return this.joinContext?.joinParams ?? null;
    }

    get presence (): PresenceOf<Schema> {
        return this.user.presence;
    }

    get assigns (): AssignsOf<Schema> {
        return this.user.assigns;
    }

    get user (): UserData<PresenceOf<Schema>, AssignsOf<Schema>> {
        if (this.connectionContext) {
            const user: UserData<PresenceOf<Schema>, AssignsOf<Schema>> = {
                id: this.connectionContext.clientId,
                assigns: {} as AssignsOf<Schema>,
                presence: {} as PresenceOf<Schema>,
            };

            return user;
        } else if (this.leaveEvent) {
            return this.leaveEvent.user;
        }

        const user = this.joinContext?.user ?? this.eventContext?.user ?? this.outgoingContext?.user;

        if (!user) {
            throw new Error('PondSocket context does not contain a user');
        }

        return user;
    }

    get channel () {
        return this.joinContext?.channel ?? this.eventContext?.channel ?? this.outgoingContext?.channel ?? this.leaveEvent?.channel ?? null;
    }

    get event () {
        if (this.connectionContext) {
            const event: PondEvent<string> = {
                params: this.connectionContext.params,
                query: this.connectionContext.query,
                payload: {},
                event: 'CONNECTION',
            };

            return event;
        }

        return this.joinContext?.event ?? this.eventContext?.event ?? this.outgoingContext?.event ?? null;
    }

    getClass () {
        return this.#instance.constructor;
    }

    getHandler () {
        return this.#instance[this.#propertyKey];
    }

    getInstance () {
        return this.#instance;
    }

    getMethod () {
        return this.#propertyKey;
    }

    accept (assigns?: Partial<AssignsOf<Schema>>): this {
        const context = this.joinContext ?? this.connectionContext;

        if (!context) {
            throw new Error('accept() is only available for connection and join handlers');
        }

        context.accept(assigns);

        return this;
    }

    decline (message?: string, status?: number): this {
        const context = this.joinContext ?? this.connectionContext;

        if (!context) {
            throw new Error('decline() is only available for connection and join handlers');
        }

        context.decline(message, status);

        return this;
    }

    assign (assigns: Partial<AssignsOf<Schema>>): this {
        const context = this.joinContext ?? this.eventContext ?? this.connectionContext;

        if (!context) {
            throw new Error('assign() is not available for this handler');
        }

        context.assign(assigns);

        return this;
    }

    reply(payload: EventReplyPayload<EventsOf<Schema>, EventName>): this;

    reply<Event extends Extract<keyof EventsOf<Schema>, string>> (
        event: Event,
        payload: EventReplyPayload<EventsOf<Schema>, Event>,
    ): this;

    reply (
        eventOrPayload: string | PondMessage,
        payload?: PondMessage,
    ): this {
        const context = this.joinContext ?? this.eventContext ?? this.connectionContext;

        if (!context) {
            throw new Error('reply() is not available for this handler');
        }

        if (typeof eventOrPayload !== 'string') {
            if (!this.eventContext) {
                throw new Error('reply(payload) is only available for event handlers');
            }

            const replyToCurrentEvent = this.eventContext.reply.bind(this.eventContext) as (
                event: string,
                responsePayload: PondMessage,
            ) => unknown;

            replyToCurrentEvent(
                this.eventContext.event.event,
                eventOrPayload,
            );

            return this;
        }

        const reply = context.reply.bind(context) as (event: string, responsePayload: PondMessage) => unknown;

        reply(eventOrPayload, payload ?? {});

        return this;
    }

    broadcast<Event extends Extract<keyof EventsOf<Schema>, string>> (
        event: Event,
        payload: EventPayload<EventsOf<Schema>, Event>,
    ): this {
        this.#getChannelContext('broadcast').broadcast(event, payload);

        return this;
    }

    broadcastFrom<Event extends Extract<keyof EventsOf<Schema>, string>> (
        event: Event,
        payload: EventPayload<EventsOf<Schema>, Event>,
    ): this {
        this.#getChannelContext('broadcastFrom').broadcastFrom(event, payload);

        return this;
    }

    broadcastTo<Event extends Extract<keyof EventsOf<Schema>, string>> (
        event: Event,
        payload: EventPayload<EventsOf<Schema>, Event>,
        users: string | string[],
    ): this {
        this.#getChannelContext('broadcastTo').broadcastTo(event, payload, users);

        return this;
    }

    trackPresence (presence: PresenceOf<Schema>): this {
        const context = this.joinContext ?? this.eventContext;

        if (!context) {
            throw new Error('trackPresence() is only available for join and event handlers');
        }

        context.trackPresence(presence);

        return this;
    }

    updatePresence (presence: Partial<PresenceOf<Schema>>): this {
        if (!this.eventContext) {
            throw new Error('updatePresence() is only available for event handlers');
        }

        this.eventContext.updatePresence(presence);

        return this;
    }

    evictUser (reason: string, userId?: string): this {
        if (!this.eventContext) {
            throw new Error('evictUser() is only available for event handlers');
        }

        this.eventContext.evictUser(reason, userId);

        return this;
    }

    removePresence (userId?: string): this {
        if (!this.eventContext) {
            throw new Error('removePresence() is only available for event handlers');
        }

        this.eventContext.removePresence(userId);

        return this;
    }

    transform (payload: EventPayload<EventsOf<Schema>, EventName> | EventReplyPayload<EventsOf<Schema>, EventName>): this {
        if (!this.outgoingContext) {
            throw new Error('transform() is only available for outgoing handlers');
        }

        this.outgoingContext.transform(payload);

        return this;
    }

    block (): this {
        if (!this.outgoingContext) {
            throw new Error('block() is only available for outgoing handlers');
        }

        this.outgoingContext.block();

        return this;
    }

    addData (key: string, value: unknown) {
        this.#data[key] = value;
    }

    getData (key: string) {
        return this.#data[key] ?? null;
    }

    #getChannelContext (method: string) {
        const context = this.joinContext ?? this.eventContext;

        if (!context) {
            throw new Error(`${method}() is only available for join and event handlers`);
        }

        return context;
    }

}
