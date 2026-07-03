import {
    ChannelReceivers,
    AnyPondSchema,
    Event,
    EventParams,
    EventPayload,
    EventsOf,
    PondMessage,
    PondObject,
} from '@eleven-am/pondsocket-common';

import { BaseContext } from './baseContext';
import { ChannelEngine } from '../engines/channelEngine';


export class OutgoingContext<
    Path extends string,
    Schema extends AnyPondSchema = AnyPondSchema,
    EventName extends Extract<keyof EventsOf<Schema>, string> = Extract<keyof EventsOf<Schema>, string>,
> extends BaseContext<Path, Schema, EventName, EventPayload<EventsOf<Schema>, EventName>> {
    #payload: EventPayload<EventsOf<Schema>, EventName>;

    #isBlocked: boolean;

    constructor (event: Event, params: EventParams<Path>, engine: ChannelEngine, userid: string) {
        super(engine, params, event.event as EventName, event.payload as EventPayload<EventsOf<Schema>, EventName>, userid);
        this.#payload = event.payload as EventPayload<EventsOf<Schema>, EventName>;
        this.#isBlocked = false;
    }

    get payload (): EventPayload<EventsOf<Schema>, EventName> {
        return this.#payload;
    }

    /**
     * Blocks the outgoing context, preventing further processing of the event.
     */
    block (): this {
        this.#isBlocked = true;

        return this;
    }

    /**
     * Checks if the outgoing context is blocked.
     * @returns {boolean} - True if blocked, false otherwise.
     */
    isBlocked (): boolean {
        return this.#isBlocked;
    }

    /**
     * Transforms the outgoing context with a new payload.
     * @param payload - The new payload to set for the context.
     */
    transform (payload: EventPayload<EventsOf<Schema>, EventName>): this {
        this.#payload = payload;

        return this;
    }

    /**
     * Updates the parameters of the outgoing context.
     * @param params - The new parameters to set for the context.
     */
    updateParams (params: EventParams<Path>) {
        super.updateParams(params);
    }

    protected _sendMessageToRecipients (_recipient: ChannelReceivers, _event: string, _payload: PondObject): void {
        void 0;
    }
}
