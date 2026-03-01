import {
    PondPresence,
    PresenceEventTypes,
    ServerActions,
    uuid,
} from '@eleven-am/pondsocket-common';

import { InternalChannelEvent } from '../abstracts/types';

export type PublishCallback = (event: InternalChannelEvent) => void;

export class PresenceEngine {
    #presenceCache: Map<string, PondPresence> = new Map();

    readonly #publish: PublishCallback;

    readonly #channelId: string;

    constructor (channelId: string, publish: PublishCallback) {
        this.#channelId = channelId;
        this.#publish = publish;
    }

    get presenceCount (): number {
        return this.#presenceCache.size;
    }

    trackPresence (userId: string, data: PondPresence): void {
        return this.#processPresenceAction(PresenceEventTypes.JOIN, userId, data);
    }

    updatePresence (userId: string, data: PondPresence): void {
        return this.#processPresenceAction(PresenceEventTypes.UPDATE, userId, data);
    }

    removePresence (userId: string): void {
        return this.#processPresenceAction(PresenceEventTypes.LEAVE, userId, null);
    }

    upsertPresence (userId: string, data: PondPresence): void {
        if (this.#presenceCache.has(userId)) {
            return this.updatePresence(userId, data);
        }

        return this.trackPresence(userId, data);
    }

    getPresence (userId: string): PondPresence | null {
        return this.#presenceCache.get(userId) || null;
    }

    getAllPresence (): Map<string, PondPresence> {
        return new Map(this.#presenceCache);
    }

    close (): void {
        this.#presenceCache.clear();
    }

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

        this.#publish(internalEvent);
    }
}
