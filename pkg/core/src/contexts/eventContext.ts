import {
    ChannelReceiver,
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


/**
 * EventContext combines the functionality of EventRequest and EventResponse
 * to provide a unified interface for handling events in a channel.
 */
export class EventContext<Path extends string> extends BaseContext<Path> {
    readonly #event: BroadcastEvent;

    readonly #requestId: string;

    constructor (event: BroadcastEvent, params: EventParams<Path>, engine: ChannelEngine) {
        super(engine, params, event.event, event.payload, event.sender);
        this.#event = event;
        this.#requestId = event.requestId;
    }

    /**
     * Assigns data to the client
     */
    assign (assigns: PondAssigns): EventContext<Path> {
        this.channel.updateAssigns(this.#event.sender, assigns);

        return this;
    }

    /**
     * Sends a direct reply to the client
     */
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

    /**
     * Broadcasts a message to all users in the channel
     */
    broadcast (event: string, payload: PondMessage): EventContext<Path> {
        this.#sendMessage(ChannelReceiver.ALL_USERS, event, payload);

        return this;
    }

    /**
     * Broadcasts a message to all users except the sender
     */
    broadcastFrom (event: string, payload: PondMessage): EventContext<Path> {
        this.#sendMessage(ChannelReceiver.ALL_EXCEPT_SENDER, event, payload);

        return this;
    }

    /**
     * Broadcasts a message to specific users
     */
    broadcastTo (event: string, payload: PondMessage, userIds: string | string[]): EventContext<Path> {
        const ids = Array.isArray(userIds) ? userIds : [userIds];

        this.#sendMessage(ids, event, payload);

        return this;
    }

    /**
     * Tracks a user's presence
     */
    trackPresence (presence: PondPresence, userId = this.#event.sender): EventContext<Path> {
        this.channel.trackPresence(userId, presence);

        return this;
    }

    /**
     * Updates a user's presence
     */
    updatePresence (presence: PondPresence, userId = this.#event.sender): EventContext<Path> {
        this.channel.updatePresence(userId, presence);

        return this;
    }

    /**
     * Kicks a user from the channel
     */
    evictUser (reason: string, userId = this.#event.sender): EventContext<Path> {
        this.channel.evictUser(userId, reason);

        return this;
    }

    /**
     * Removes a user's presence
     */
    removePresence (userId = this.#event.sender): EventContext<Path> {
        this.channel.removePresence(userId);

        return this;
    }

    /**
     * Sends a message to recipients
     */
    #sendMessage (recipient: ChannelReceivers, event: string, payload: PondObject) {
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
