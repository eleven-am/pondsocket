import {
    SystemSender,
    ChannelReceiver,
    AnyPondSchema,
    AssignsOf,
    EventsOf,
    EventPayload,
    ServerActions,
    PondMessage,
    PresenceOf,
    UserData,
    UserAssigns,
    UserPresences,
} from '@eleven-am/pondsocket-common';

import { ChannelEngine } from '../engines/channelEngine';

export class Channel<Schema extends AnyPondSchema = AnyPondSchema> {
    readonly #engine: ChannelEngine;

    constructor (engine: ChannelEngine) {
        this.#engine = engine;
    }

    /**
     * Gets a user's data
     */
    getUserData (userId: string): UserData<PresenceOf<Schema>, AssignsOf<Schema>> | null {
        return this.#engine.getUserData(userId) as UserData<PresenceOf<Schema>, AssignsOf<Schema>> | null;
    }

    /**
     * Gets all presence data
     */
    getPresences (): UserPresences<PresenceOf<Schema>> {
        return this.#engine.getPresence() as UserPresences<PresenceOf<Schema>>;
    }

    /**
     * Gets all assigns data
     */
    getAssigns (): UserAssigns<AssignsOf<Schema>> {
        return this.#engine.getAssigns() as UserAssigns<AssignsOf<Schema>>;
    }

    /**
     * Broadcasts a message to all users
     */
    broadcast<Event extends Extract<keyof EventsOf<Schema>, string>> (event: Event, payload: EventPayload<EventsOf<Schema>, Event>) {
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
    broadcastFrom<Event extends Extract<keyof EventsOf<Schema>, string>> (userId: string, event: Event, payload: EventPayload<EventsOf<Schema>, Event>) {
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
    broadcastTo<Event extends Extract<keyof EventsOf<Schema>, string>> (userIds: string | string[], event: Event, payload: EventPayload<EventsOf<Schema>, Event>) {
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
    trackPresence (userId: string, presence: PresenceOf<Schema>) {
        this.#engine.trackPresence(userId, presence);

        return this;
    }

    /**
     * Updates a user's presence
     */
    updatePresence (userId: string, presence: Partial<PresenceOf<Schema>>) {
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
    upsertPresence (userId: string, presence: PresenceOf<Schema>) {
        this.#engine.upsertPresence(userId, presence);

        return this;
    }

    /**
     * Updates a user's assigns
     */
    updateAssigns (userId: string, assigns: Partial<AssignsOf<Schema>>) {
        this.#engine.updateAssigns(userId, assigns);

        return this;
    }

    /**
     * Resolves a user's data from any node in the distributed cluster
     */
    getUserAcrossNodes (userId: string, timeoutMs?: number): Promise<UserData<PresenceOf<Schema>, AssignsOf<Schema>> | null> {
        return this.#engine.getUserAcrossNodes(userId, timeoutMs) as Promise<UserData<PresenceOf<Schema>, AssignsOf<Schema>> | null>;
    }

    /**
     * Requests removal of a user across all nodes in the distributed cluster
     */
    requestUserRemoval (userId: string) {
        this.#engine.requestUserRemoval(userId);

        return this;
    }
}
