import { Event, EventParams, PondMessage } from '@eleven-am/pondsocket-common';

import { BaseContext } from './baseContext';
import { ChannelEngine } from '../engines/channelEngine';


export class OutgoingContext<Path extends string> extends BaseContext<Path> {
    #payload: PondMessage;

    #isBlocked: boolean;

    constructor (event: Event, params: EventParams<Path>, engine: ChannelEngine, userid: string) {
        super(engine, params, event.event, event.payload, userid);
        this.#payload = event.payload;
        this.#isBlocked = false;
    }

    get payload (): PondMessage {
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
    transform (payload: PondMessage): this {
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
}
