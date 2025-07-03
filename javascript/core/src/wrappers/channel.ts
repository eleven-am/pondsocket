import {
    SystemSender,
    ChannelReceiver,
    ServerActions,
    PondMessage,
    PondAssigns,
    PondPresence,
} from '@eleven-am/pondsocket-common';

import { ChannelEngine } from '../engines/channelEngine';

export class Channel {
    readonly #engine: ChannelEngine;

    constructor (engine: ChannelEngine) {
        this.#engine = engine;
    }

    /**
     * Gets a user's data
     */
    getUserData (userId: string) {
        return this.#engine.getUserData(userId);
    }

    /**
     * Gets all presence data
     */
    getPresences () {
        return this.#engine.getPresence();
    }

    /**
     * Gets all assigns data
     */
    getAssigns () {
        return this.#engine.getAssigns();
    }

    /**
     * Broadcasts a message to all users
     */
    broadcast (event: string, payload: PondMessage) {
        this.#engine.sendMessage(
            SystemSender.CHANNEL,
            ChannelReceiver.ALL_USERS,
            ServerActions.BROADCAST,
            event,
            payload,
        );

        return this;
    }

    /**
     * Broadcasts a message from a specific user to all others
     */
    broadcastFrom (userId: string, event: string, payload: PondMessage) {
        this.#engine.sendMessage(
            userId,
            ChannelReceiver.ALL_EXCEPT_SENDER,
            ServerActions.BROADCAST,
            event,
            payload,
        );

        return this;
    }

    /**
     * Broadcasts a message to specific users
     */
    broadcastTo (userIds: string | string[], event: string, payload: PondMessage) {
        const users = Array.isArray(userIds) ? userIds : [userIds];

        this.#engine.sendMessage(
            SystemSender.CHANNEL,
            users,
            ServerActions.BROADCAST,
            event,
            payload,
        );

        return this;
    }

    /**
     * Kicks a user from the channel
     */
    evictUser (userId: string, reason?: string) {
        this.#engine.kickUser(userId, reason ?? 'You have been banned from the channel');

        return this;
    }

    /**
     * Tracks a user's presence
     */
    trackPresence (userId: string, presence: PondPresence) {
        this.#engine.trackPresence(userId, presence);

        return this;
    }

    /**
     * Updates a user's presence
     */
    updatePresence (userId: string, presence: PondPresence) {
        this.#engine.updatePresence(userId, presence);

        return this;
    }

    /**
     * Removes a user's presence
     */
    removePresence (userId: string) {
        this.#engine.removePresence(userId);

        return this;
    }

    /**
     * Adds or updates a user's presence
     */
    upsertPresence (userId: string, presence: PondPresence) {
        this.#engine.upsertPresence(userId, presence);

        return this;
    }

    /**
     * Updates a user's assigns
     */
    updateAssigns (userId: string, assigns: PondAssigns) {
        this.#engine.updateAssigns(userId, assigns);

        return this;
    }
}
