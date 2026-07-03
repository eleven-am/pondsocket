import {
    ChannelReceiver,
    ChannelReceivers,
    AnyPondSchema,
    AssignsOf,
    EventParams,
    EventPayload,
    EventsOf,
    PondEvent,
    PondMessage,
    PondObject,
    PresenceOf,
    UserAssigns,
    UserPresences,
} from '@eleven-am/pondsocket-common';

import { ChannelEngine } from '../engines/channelEngine';
import { Channel } from '../wrappers/channel';

export abstract class BaseContext<
    Path extends string,
    Schema extends AnyPondSchema = AnyPondSchema,
    EventName extends Extract<keyof EventsOf<Schema>, string> = Extract<keyof EventsOf<Schema>, string>,
    Payload extends PondMessage = EventPayload<EventsOf<Schema>, EventName>,
> {
    readonly #engine: ChannelEngine;

    #params: EventParams<Path>;

    readonly #event: string;

    readonly #sender: string;

    readonly #payload: Payload;

    protected constructor (
        engine: ChannelEngine,
        params: EventParams<Path>,
        event: string,
        payload: Payload,
        sender: string,
    ) {
        this.#engine = engine;
        this.#params = params;
        this.#event = event;
        this.#payload = payload;
        this.#sender = sender;
    }

    get event (): PondEvent<Path> & { event: EventName, payload: Payload } {
        return {
            event: this.#event as EventName,
            query: this.#params.query,
            params: this.#params.params,
            payload: this.#payload,
        };
    }

    get query (): EventParams<Path>['query'] {
        return this.#params.query;
    }

    get params (): EventParams<Path>['params'] {
        return this.#params.params;
    }

    get channelName (): string {
        return this.#engine.name;
    }

    get channel (): Channel<Schema> {
        return new Channel<Schema>(this.#engine);
    }

    get presences (): UserPresences<PresenceOf<Schema>> {
        return this.#engine.getPresence();
    }

    get assigns (): UserAssigns<AssignsOf<Schema>> {
        return this.#engine.getAssigns();
    }

    get user () {
        return this.channel.getUserData(this.#sender);
    }

    broadcast<Event extends Extract<keyof EventsOf<Schema>, string>> (event: Event, payload: EventPayload<EventsOf<Schema>, Event>): this {
        this._sendMessageToRecipients(ChannelReceiver.ALL_USERS, event, payload);

        return this;
    }

    broadcastFrom<Event extends Extract<keyof EventsOf<Schema>, string>> (event: Event, payload: EventPayload<EventsOf<Schema>, Event>): this {
        this._sendMessageToRecipients(ChannelReceiver.ALL_EXCEPT_SENDER, event, payload);

        return this;
    }

    broadcastTo<Event extends Extract<keyof EventsOf<Schema>, string>> (event: Event, payload: EventPayload<EventsOf<Schema>, Event>, userIds: string | string[]): this {
        const ids = Array.isArray(userIds) ? userIds : [userIds];

        this._sendMessageToRecipients(ids, event, payload);

        return this;
    }

    protected abstract _sendMessageToRecipients (recipient: ChannelReceivers, event: string, payload: PondObject): void;

    protected get engine (): ChannelEngine {
        return this.#engine;
    }

    protected get sender (): string {
        return this.#sender;
    }

    protected updateParams (params: EventParams<Path>): void {
        this.#params = params;
    }
}
