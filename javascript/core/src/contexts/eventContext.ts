import {
    ChannelReceivers,
    EventParams,
    PondAssigns,
    PondMessage,
    PondObject,
    PondPresence,
    ServerActions,
    SystemSender,
} from '@eleven-am/pondsocket-common';

import { BaseContext } from './baseContext';
import { BroadcastEvent } from '../abstracts/types';
import { ChannelEngine } from '../engines/channelEngine';

export class EventContext<Path extends string> extends BaseContext<Path> {
    readonly #event: BroadcastEvent;

    readonly #requestId: string;

    constructor (event: BroadcastEvent, params: EventParams<Path>, engine: ChannelEngine) {
        super(engine, params, event.event, event.payload, event.sender);
        this.#event = event;
        this.#requestId = event.requestId;
    }

    assign (assigns: PondAssigns): EventContext<Path> {
        this.channel.updateAssigns(this.#event.sender, assigns);

        return this;
    }

    reply (event: string, payload: PondMessage) {
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

    trackPresence (presence: PondPresence, userId = this.#event.sender): EventContext<Path> {
        this.channel.trackPresence(userId, presence);

        return this;
    }

    updatePresence (presence: PondPresence, userId = this.#event.sender): EventContext<Path> {
        this.channel.updatePresence(userId, presence);

        return this;
    }

    evictUser (reason: string, userId = this.#event.sender): EventContext<Path> {
        this.channel.evictUser(userId, reason);

        return this;
    }

    removePresence (userId = this.#event.sender): EventContext<Path> {
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
