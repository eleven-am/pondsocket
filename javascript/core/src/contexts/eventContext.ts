import {
    ChannelReceivers,
    AnyPondSchema,
    AssignsOf,
    EventParams,
    EventPayload,
    EventReplyPayload,
    EventsOf,
    PondObject,
    PresenceOf,
    ServerActions,
    SystemSender,
} from '@eleven-am/pondsocket-common';

import { BaseContext } from './baseContext';
import { BroadcastEvent } from '../abstracts/types';
import { ChannelEngine } from '../engines/channelEngine';

export class EventContext<
    Path extends string,
    Schema extends AnyPondSchema = AnyPondSchema,
    EventName extends Extract<keyof EventsOf<Schema>, string> = Extract<keyof EventsOf<Schema>, string>,
> extends BaseContext<Path, Schema, EventName, EventPayload<EventsOf<Schema>, EventName>> {
    readonly #event: BroadcastEvent;

    readonly #requestId: string;

    constructor (event: BroadcastEvent, params: EventParams<Path>, engine: ChannelEngine) {
        super(engine, params, event.event as EventName, event.payload as EventPayload<EventsOf<Schema>, EventName>, event.sender);
        this.#event = event;
        this.#requestId = event.requestId;
    }

    get payload (): EventPayload<EventsOf<Schema>, EventName> {
        return this.#event.payload as EventPayload<EventsOf<Schema>, EventName>;
    }

    assign (assigns: Partial<AssignsOf<Schema>>): EventContext<Path, Schema, EventName> {
        this.channel.updateAssigns(this.#event.sender, assigns);

        return this;
    }

    reply<Event extends Extract<keyof EventsOf<Schema>, string>> (event: Event, payload: EventReplyPayload<EventsOf<Schema>, Event>) {
        this.engine.sendMessage(
            SystemSender.CHANNEL,
            [this.#event.sender],
            ServerActions.SYSTEM,
            event,
            payload,
            this.#requestId,
        );

        return this;
    }

    trackPresence (presence: PresenceOf<Schema>, userId = this.#event.sender): EventContext<Path, Schema, EventName> {
        this.channel.trackPresence(userId, presence);

        return this;
    }

    updatePresence (presence: Partial<PresenceOf<Schema>>, userId = this.#event.sender): EventContext<Path, Schema, EventName> {
        this.channel.updatePresence(userId, presence);

        return this;
    }

    evictUser (reason: string, userId = this.#event.sender): EventContext<Path, Schema, EventName> {
        this.channel.evictUser(userId, reason);

        return this;
    }

    removePresence (userId = this.#event.sender): EventContext<Path, Schema, EventName> {
        this.channel.removePresence(userId);

        return this;
    }

    protected _sendMessageToRecipients (recipient: ChannelReceivers, event: string, payload: PondObject) {
        this.engine.sendMessage(
            this.#event.sender,
            recipient,
            ServerActions.BROADCAST,
            event,
            payload,
            this.#requestId,
        );
    }
}
