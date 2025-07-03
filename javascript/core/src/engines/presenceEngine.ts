import {
    PondPresence,
    PresenceEventTypes,
    ServerActions,
    Subject,
    Unsubscribe,
    uuid,
} from '@eleven-am/pondsocket-common';

import { InternalChannelEvent } from '../abstracts/types';

/**
 * Manages user presence for a channel
 */
export class PresenceEngine {
    #presenceCache: Map<string, PondPresence> = new Map();

    #publisher: Subject<InternalChannelEvent> = new Subject();

    readonly #channelId: string;

    /**
     * Creates a new Presence manager
     * @param channelId - The ID of the channel this presence belongs to
     */
    constructor (channelId: string) {
        this.#channelId = channelId;
    }

    /**
     * Gets the number of users with presence data
     */
    get presenceCount (): number {
        return this.#presenceCache.size;
    }

    /**
     * Track a new user's presence
     * @param userId - The ID of the user
     * @param data - The presence data
     * @returns The generated presence event
     */
    trackPresence (userId: string, data: PondPresence): void {
        return this.#processPresenceAction(PresenceEventTypes.JOIN, userId, data);
    }

    /**
     * Update an existing user's presence
     * @param userId - The ID of the user
     * @param data - The updated presence data
     * @returns The generated presence event
     */
    updatePresence (userId: string, data: PondPresence): void {
        return this.#processPresenceAction(PresenceEventTypes.UPDATE, userId, data);
    }

    /**
     * Remove a user's presence
     * @param userId - The ID of the user
     * @returns The generated presence event
     */
    removePresence (userId: string): void {
        return this.#processPresenceAction(PresenceEventTypes.LEAVE, userId, null);
    }

    /**
     * Add or update a user's presence
     * @param userId - The ID of the user
     * @param data - The presence data
     * @returns The generated presence event
     */
    upsertPresence (userId: string, data: PondPresence): void {
        if (this.#presenceCache.has(userId)) {
            return this.updatePresence(userId, data);
        }

        return this.trackPresence(userId, data);
    }

    /**
     * Get a specific user's presence
     * @param userId - The ID of the user
     * @returns The user's presence data or null if not found
     */
    getPresence (userId: string): PondPresence | null {
        return this.#presenceCache.get(userId) || null;
    }

    /**
     * Get presence data for all users
     * @returns A copy of all presence data
     */
    getAllPresence (): Map<string, PondPresence> {
        return new Map(this.#presenceCache);
    }

    /**
     * Subscribe to presence events
     * @param callback - The callback to invoke when presence changes
     * @returns A function to unsubscribe
     */
    subscribe (callback: (event: InternalChannelEvent) => void): Unsubscribe {
        return this.#publisher.subscribe(callback);
    }

    /**
     * Clear all presence data and close the publisher
     */
    close (): void {
        this.#presenceCache.clear();
        this.#publisher.close();
    }

    /**
     * Process presence data changes and create events
     * @param action - The action type (create, update, delete)
     * @param userId - The ID of the user
     * @param data - The presence data
     * @returns The generated presence event message
     * @throws Error if action is invalid or user doesn't exist/already exists
     */
    #processPresenceAction (action: PresenceEventTypes, userId: string, data: PondPresence | null): void {
        if (action === PresenceEventTypes.JOIN && this.#presenceCache.has(userId)) {
            throw new Error(`User with id ${userId} already exists in the presence cache`);
        } else if ((action === PresenceEventTypes.UPDATE || action === PresenceEventTypes.LEAVE) && !this.#presenceCache.has(userId)) {
            throw new Error(`User with id ${userId} does not exist in the presence cache`);
        }

        if (action !== PresenceEventTypes.LEAVE && !data) {
            throw new Error(`Data is required for ${action} action`);
        }

        const current = this.#presenceCache.get(userId);

        if (data) {
            this.#presenceCache.set(userId, data);
        } else {
            this.#presenceCache.delete(userId);
        }

        const total = Array.from(this.#presenceCache.values());
        const userIds = Array.from(this.#presenceCache.keys());

        const internalEvent: InternalChannelEvent = {
            event: action,
            requestId: uuid(),
            recipients: userIds,
            channelName: this.#channelId,
            action: ServerActions.PRESENCE,
            payload: {
                changed: current || data || {},
                presence: total,
            },
        };

        this.#publisher.publish(internalEvent);
    }
}
