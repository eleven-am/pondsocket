import {
    ChannelReceivers,
    AnyPondSchema,
    Event,
    EventParams,
    EventPayload,
    EventReplyPayload,
    EventsOf,
    PondEvent,
    PondMessage,
    PondObject,
    ServerActions,
} from '@eleven-am/pondsocket-common';

import { BaseContext } from './baseContext';
import { ChannelEngine } from '../engines/channelEngine';


export class OutgoingContext<
    Path extends string,
    Schema extends AnyPondSchema = AnyPondSchema,
    EventName extends Extract<keyof EventsOf<Schema>, string> = Extract<keyof EventsOf<Schema>, string>,
    Action extends Event['action'] = Event['action'],
> extends BaseContext<Path, Schema, EventName, OutgoingPayload<Schema, EventName, Action>> {
    readonly #action: Action;

    #isBlocked: boolean;

    constructor (event: Event, params: EventParams<Path>, engine: ChannelEngine, userid: string) {
        super(engine, params, event.event as EventName, event.payload as OutgoingPayload<Schema, EventName, Action>, userid);
        this.#action = event.action as Action;
        this.#isBlocked = false;
    }

    get action (): Action {
        return this.#action;
    }

    get event (): PondEvent<Path> & {
        action: Action;
        event: EventName;
        payload: OutgoingPayload<Schema, EventName, Action>;
    } {
        return {
            ...super.event,
            action: this.#action,
            payload: this.currentPayload,
        };
    }

    get payload (): OutgoingPayload<Schema, EventName, Action> {
        return this.currentPayload;
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
    transform (payload: OutgoingPayload<Schema, EventName, Action>): this {
        this.updatePayload(payload);

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

export type OutgoingPayload<
    Schema extends AnyPondSchema,
    EventName extends Extract<keyof EventsOf<Schema>, string>,
    Action extends Event['action'],
> = Action extends ServerActions.BROADCAST
    ? EventPayload<EventsOf<Schema>, EventName>
    : Action extends ServerActions.SYSTEM
        ? EventReplyPayload<EventsOf<Schema>, EventName>
        : PondMessage;

export type OutgoingHandlerContext<
    Path extends string,
    Schema extends AnyPondSchema,
    EventName extends Extract<keyof EventsOf<Schema>, string>,
> = OutgoingContext<Path, Schema, EventName, ServerActions.BROADCAST> |
    OutgoingContext<Path, Schema, EventName, ServerActions.SYSTEM>;
