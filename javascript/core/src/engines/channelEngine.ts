import {
    ChannelEvent,
    ChannelReceiver,
    ChannelReceivers,
    ClientMessage,
    ErrorTypes,
    Events,
    PondAssigns,
    PondMessage,
    PondPresence,
    ServerActions,
    Subject,
    SystemSender,
    Unsubscribe,
    UserAssigns,
    UserData,
    UserPresences,
    uuid,
} from '@eleven-am/pondsocket-common';

import { LobbyEngine } from './lobbyEngine';
import { PresenceEngine } from './presenceEngine';
import {
    type BroadcastEvent,
    type ChannelSenders,
    DistributedMessageType,
    type InternalChannelEvent,
} from '../abstracts/types';
import { HttpError } from '../errors/httpError';
import type {
    AssignsRemoved,
    AssignsUpdate,
    DistributedChannelMessage,
    EvictUser,
    IDistributedBackend,
    PresenceRemoved,
    PresenceUpdate,
    StateRequest,
    StateResponse,
    UserJoined,
    UserLeft,
    UserMessage,
} from '../types';

export class ChannelEngine {
    readonly #endpointId: string;

    readonly #backend: IDistributedBackend | null;

    #presenceEngine: PresenceEngine | null = null;

    #assignsCache: Map<string, PondAssigns> = new Map();

    #userSubscriptions: Map<string, Unsubscribe> = new Map();

    #publisher: Subject<InternalChannelEvent> = new Subject();

    #distributedSubscription: Unsubscribe | null = null;

    readonly #name: string;

    constructor (public parent: LobbyEngine, name: string, backend: IDistributedBackend | null = null) {
        this.#name = name;
        this.#backend = backend;
        this.#endpointId = parent.parent.path;

        if (this.#backend) {
            this.#setupDistributedSubscription();
        }
    }

    get name (): string {
        return this.#name;
    }

    get users (): Set<string> {
        return new Set(this.#assignsCache.keys());
    }

    /**
     * Adds a user to the channel
     * @param userId - The user ID
     * @param assigns - The user's assigns
     * @param onMessage - Callback for messages
     * @returns Unsubscribe function
     */
    addUser (userId: string, assigns: PondAssigns, onMessage: (event: ChannelEvent) => void): Unsubscribe {
        if (this.users.has(userId)) {
            const message = `ChannelEngine: User with id ${userId} already exists in channel ${this.name}`;

            throw new HttpError(400, message);
        }

        const isFirstUser = this.users.size === 0;

        onMessage({
            channelName: this.#name,
            requestId: uuid(),
            action: ServerActions.SYSTEM,
            event: Events.ACKNOWLEDGE,
            payload: {},
        });

        this.#assignsCache.set(userId, assigns);
        this.#buildSubscriber(userId, onMessage);
        if (isFirstUser && this.#backend) {
            this.#requestChannelState();
        }

        if (this.#backend) {
            this.#broadcastToNodes({
                type: DistributedMessageType.USER_JOINED,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
                presence: {},
                assigns,
            });
        }

        return () => this.removeUser(userId);
    }

    /**
     * Sends a message to recipients
     */
    sendMessage (
        sender: ChannelSenders,
        recipient: ChannelReceivers,
        action: ServerActions,
        event: string,
        payload: PondMessage,
        requestId: string = uuid(),
    ): void {
        if (!this.users.has(sender as string) && sender !== SystemSender.CHANNEL) {
            const message = `ChannelEngine: User with id ${sender} does not exist in channel ${this.name}`;

            throw new HttpError(404, message);
        }

        const channelEvent = {
            channelName: this.name,
            requestId,
            action,
            event,
            payload,
        } as ChannelEvent;

        const recipients = this.#getUsersFromRecipients(recipient, sender);

        const internalEvent: InternalChannelEvent = {
            ...channelEvent,
            recipients,
        };

        this.#publisher.publish(internalEvent);

        if (this.#backend) {
            this.#broadcastToNodes({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: this.#endpointId,
                channelName: this.#name,
                fromUserId: sender as string,
                event,
                payload,
                requestId,
                recipients,
            });
        }
    }

    /**
     * Broadcasts a message from a user
     * @param userId - The ID of the user sending the message
     * @param message - The message to broadcast
     */
    broadcastMessage (userId: string, message: ClientMessage): void {
        if (!this.users.has(userId)) {
            const messageText = `ChannelEngine: User with id ${userId} does not exist in channel ${this.name}`;

            throw new HttpError(404, messageText);
        }

        const responseEvent: BroadcastEvent = {
            ...message,
            sender: userId,
            action: ServerActions.BROADCAST,
        };

        this.parent.middleware.run(responseEvent, this, (error) => {
            this.sendMessage(
                SystemSender.CHANNEL,
                [userId],
                ServerActions.ERROR,
                ErrorTypes.HANDLER_NOT_FOUND,
                {
                    message: error?.message || 'A handler did not respond to the event',
                    code: error?.statusCode || 500,
                },
                message.requestId,
            );
        });
    }

    /**
     * Tracks a user's presence
     * @param userId - The ID of the user
     * @param presence - The presence data to track
     */
    trackPresence (userId: string, presence: PondPresence): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.trackPresence(userId, presence);

        if (this.#backend) {
            this.#broadcastToNodes({
                type: DistributedMessageType.PRESENCE_UPDATE,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
                presence,
            });
        }
    }

    /**
     * Updates a user's presence
     * @param userId - The ID of the user
     * @param presence - The presence data to update
     */
    updatePresence (userId: string, presence: PondPresence): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.updatePresence(userId, presence);

        if (this.#backend) {
            this.#broadcastToNodes({
                type: DistributedMessageType.PRESENCE_UPDATE,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
                presence,
            });
        }
    }

    /**
     * Removes a user's presence
     * @param userId - The ID of the user to remove presence for
     */
    removePresence (userId: string): void {
        if (this.#presenceEngine) {
            this.#presenceEngine.removePresence(userId);

            // Broadcast presence removal to other nodes
            if (this.#backend) {
                this.#broadcastToNodes({
                    type: DistributedMessageType.PRESENCE_REMOVED,
                    endpointName: this.#endpointId,
                    channelName: this.#name,
                    userId,
                });
            }
        }
    }

    /**
     * Adds or updates a user's presence
     */
    upsertPresence (userId: string, presence: PondPresence): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.upsertPresence(userId, presence);

        // Broadcast presence update to other nodes
        if (this.#backend) {
            this.#broadcastToNodes({
                type: DistributedMessageType.PRESENCE_UPDATE,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
                presence,
            });
        }
    }

    /**
     * Updates a user's assigns
     */
    updateAssigns (userId: string, assigns: PondMessage): void {
        if (!this.#assignsCache.has(userId)) {
            throw new HttpError(404, `User with id ${userId} does not exist in channel ${this.name}`);
        }

        const currentAssigns = this.#assignsCache.get(userId) || {};
        const newAssigns = {
            ...currentAssigns,
            ...assigns,
        };

        this.#assignsCache.set(userId, newAssigns);

        // Broadcast assigns update to other nodes
        if (this.#backend) {
            this.#broadcastToNodes({
                type: DistributedMessageType.ASSIGNS_UPDATE,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
                assigns: newAssigns,
            });
        }
    }

    /**
     * Kicks a user from the channel
     */
    kickUser (userId: string, reason: string): void {
        this.sendMessage(SystemSender.CHANNEL, [userId], ServerActions.SYSTEM, 'kicked_out', {
            message: reason,
            code: 403,
        });

        // Broadcast eviction to other nodes
        if (this.#backend) {
            this.#broadcastToNodes({
                type: DistributedMessageType.EVICT_USER,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
                reason,
            });
        }

        this.removeUser(userId);
        this.sendMessage(SystemSender.CHANNEL, ChannelReceiver.ALL_USERS, ServerActions.SYSTEM, 'kicked', {
            userId,
            reason,
        });
    }

    /**
     * Gets all assigns as an object
     */
    getAssigns (): UserAssigns {
        return Array
            .from(this.#assignsCache.entries())
            .reduce((acc, [id, assigns]) => {
                acc[id] = assigns;

                return acc;
            }, {} as UserAssigns);
    }

    /**
     * Gets all presence data as an object
     */
    getPresence (): UserPresences {
        if (!this.#presenceEngine) {
            return {};
        }

        return Array
            .from(this.#presenceEngine.getAllPresence().entries())
            .reduce((acc, [id, presence]) => {
                acc[id] = presence;

                return acc;
            }, {} as UserPresences);
    }

    /**
     * Destroys the channel
     */
    destroy (reason?: string): void {
        this.sendMessage(SystemSender.CHANNEL, ChannelReceiver.ALL_USERS, ServerActions.ERROR, 'destroyed', {
            message: reason ?? 'Channel has been destroyed',
        });

        this.close();
    }

    /**
     * Removes a user from the channel
     */
    removeUser (userId: string): void {
        try {
            const userData = this.getUserData(userId);
            const unsubscribe = this.#userSubscriptions.get(userId);

            this.#assignsCache.delete(userId);
            if (this.#presenceEngine) {
                this.#presenceEngine.removePresence(userId);
            }

            if (unsubscribe) {
                unsubscribe();
                this.#userSubscriptions.delete(userId);
            }

            // Broadcast user left to other nodes
            if (this.#backend) {
                this.#broadcastToNodes({
                    type: DistributedMessageType.USER_LEFT,
                    endpointName: this.#endpointId,
                    channelName: this.#name,
                    userId,
                });
            }

            if (this.parent.leaveCallback) {
                this.parent.leaveCallback({
                    user: userData,
                    channel: this.parent.wrapChannel(this),
                });
            }

            if (this.users.size === 0) {
                this.close();
            }
        } catch {
            // Ignore cleanup errors
        }
    }

    /**
     * Gets user data by ID
     */
    getUserData (userId: string): UserData {
        const assigns = this.#assignsCache.get(userId);
        const presence = this.#presenceEngine?.getPresence(userId) || null;

        if (!assigns && !presence) {
            const message = `User with id ${userId} does not exist in the channel ${this.name}`;

            throw new HttpError(404, message);
        }

        return {
            assigns: assigns || {},
            presence: presence || {},
            id: userId,
        };
    }

    /**
     * Closes the channel and cleans up resources
     */
    close (): void {
        this.#userSubscriptions.forEach((unsubscribe) => unsubscribe());
        this.#userSubscriptions.clear();

        this.#assignsCache.clear();

        if (this.#presenceEngine) {
            this.#presenceEngine.close();
            this.#presenceEngine = null;
        }

        if (this.#distributedSubscription) {
            this.#distributedSubscription();
            this.#distributedSubscription = null;
        }

        this.#publisher.close();

        this.parent.deleteChannel(this.name);
    }

    /**
     * Builds a subscriber for a user
     */
    #buildSubscriber (userId: string, onMessage: (event: ChannelEvent) => void): void {
        const subscription = this.#publisher.subscribe(async ({ recipients, ...event }) => {
            if (recipients.includes(userId)) {
                if (event.action === ServerActions.PRESENCE) {
                    return onMessage(event);
                }

                const newEvent = await this.parent.processOutgoingEvents(event, this, userId);

                if (newEvent) {
                    onMessage(newEvent);
                }
            }
        });

        this.#userSubscriptions.set(userId, subscription);
    }

    /**
     * Gets or creates the presence engine
     */
    #getOrCreatePresenceEngine (): PresenceEngine {
        if (!this.#presenceEngine) {
            this.#presenceEngine = new PresenceEngine(this.name);

            this.#presenceEngine.subscribe((event) => {
                this.#publisher.publish(event);
            });
        }

        return this.#presenceEngine;
    }

    /**
     * Gets users from recipient's specification
     */
    #getUsersFromRecipients (recipients: ChannelReceivers, sender: ChannelSenders): string[] {
        const allUsers = Array.from(this.users);
        let users: string[];

        switch (recipients) {
            case ChannelReceiver.ALL_USERS:
                users = allUsers;
                break;

            case ChannelReceiver.ALL_EXCEPT_SENDER:
                if (sender === SystemSender.CHANNEL) {
                    const message = `ChannelEngine: Invalid sender ${sender} for recipients ${recipients}`;

                    throw new HttpError(400, message);
                }
                users = allUsers.filter((user) => user !== sender);
                break;

            default:
                if (!Array.isArray(recipients)) {
                    const message = `ChannelEngine: Invalid recipients ${recipients} must be an array of user ids or ${ChannelReceiver.ALL_USERS}`;

                    throw new HttpError(400, message);
                }

                if (recipients.some((user) => !allUsers.includes(user))) {
                    const message = `ChannelEngine: Invalid user ids in recipients ${recipients}`;

                    throw new HttpError(400, message);
                }

                users = recipients;
                break;
        }

        return users;
    }

    /**
     * Setup distributed subscription to listen for messages from other nodes
     */
    #setupDistributedSubscription (): void {
        if (!this.#backend) {
            return;
        }

        this.#distributedSubscription = this.#backend.subscribe(this.#endpointId, this.#name, (message) => {
            this.#handleDistributedMessage(message);
        });
    }

    /**
     * Handle messages from other nodes
     */
    #handleDistributedMessage (message: DistributedChannelMessage): void {
        switch (message.type) {
            case DistributedMessageType.STATE_REQUEST:
                this.#handleStateRequest(message);
                break;
            case DistributedMessageType.STATE_RESPONSE:
                this.#handleStateResponse(message);
                break;
            case DistributedMessageType.USER_JOINED:
                this.#handleRemoteUserJoined(message);
                break;
            case DistributedMessageType.USER_LEFT:
                this.#handleRemoteUserLeft(message);
                break;
            case DistributedMessageType.USER_MESSAGE:
                this.#handleRemoteMessage(message);
                break;
            case DistributedMessageType.PRESENCE_UPDATE:
                this.#handleRemotePresenceUpdate(message);
                break;
            case DistributedMessageType.PRESENCE_REMOVED:
                this.#handleRemotePresenceRemoved(message);
                break;
            case DistributedMessageType.ASSIGNS_UPDATE:
                this.#handleRemoteAssignsUpdate(message);
                break;
            case DistributedMessageType.ASSIGNS_REMOVED:
                this.#handleRemoteAssignsRemoved(message);
                break;
            case DistributedMessageType.EVICT_USER:
                this.#handleRemoteEvictUser(message);
                break;
            default:
                break;
        }
    }

    /**
     * Request current channel state from other nodes
     */
    #requestChannelState (): void {
        if (!this.#backend) {
            return;
        }

        this.#broadcastToNodes({
            type: DistributedMessageType.STATE_REQUEST,
            endpointName: this.#endpointId,
            channelName: this.#name,
            fromNode: 'current-node',
        });
    }

    /**
     * Handle state request from another node
     * @param _message - The state request message
     */
    #handleStateRequest (_message: StateRequest): void {
        if (this.users.size === 0) {
            return;
        }

        const users = Array.from(this.#assignsCache.entries()).map(([id, assigns]) => ({
            id,
            assigns,
            presence: this.#presenceEngine?.getPresence(id) || {},
        }));

        this.#broadcastToNodes({
            type: DistributedMessageType.STATE_RESPONSE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            users,
        });
    }

    /**
     * Handle state response from another node
     */
    #handleStateResponse (message: StateResponse): void {
        if (!message.users) {
            return;
        }

        message.users.forEach((user) => {
            if (!this.users.has(user.id)) {
                this.#assignsCache.set(user.id, user.assigns);

                if (user.presence && Object.keys(user.presence).length > 0) {
                    const presenceEngine = this.#getOrCreatePresenceEngine();

                    presenceEngine.trackPresence(user.id, user.presence);
                }
            }
        });
    }

    /**
     * Handle remote user joined
     */
    #handleRemoteUserJoined (message: UserJoined): void {
        if (this.users.has(message.userId)) {
            return;
        }

        this.#assignsCache.set(message.userId, message.assigns);

        if (message.presence && Object.keys(message.presence).length > 0) {
            const presenceEngine = this.#getOrCreatePresenceEngine();

            presenceEngine.trackPresence(message.userId, message.presence);
        }
    }

    /**
     * Handle remote user left
     */
    #handleRemoteUserLeft (message: UserLeft): void {
        this.#assignsCache.delete(message.userId);

        if (this.#presenceEngine) {
            this.#presenceEngine.removePresence(message.userId);
        }
    }

    /**
     * Handle a remote message
     */
    #handleRemoteMessage (message: UserMessage): void {
        const internalEvent: InternalChannelEvent = {
            channelName: this.#name,
            requestId: message.requestId,
            action: ServerActions.BROADCAST,
            event: message.event,
            payload: message.payload,
            recipients: message.recipients,
        };

        this.#publisher.publish(internalEvent);
    }

    /**
     * Handle remote presence update
     */
    #handleRemotePresenceUpdate (message: PresenceUpdate): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.upsertPresence(message.userId, message.presence);
    }

    /**
     * Handle remote presence removed
     */
    #handleRemotePresenceRemoved (message: PresenceRemoved): void {
        if (this.#presenceEngine) {
            this.#presenceEngine.removePresence(message.userId);
        }
    }

    /**
     * Handle remote assigns update
     */
    #handleRemoteAssignsUpdate (message: AssignsUpdate): void {
        this.#assignsCache.set(message.userId, message.assigns);
    }

    /**
     * Handle remote assigns removed
     */
    #handleRemoteAssignsRemoved (message: AssignsRemoved): void {
        this.#assignsCache.set(message.userId, {});
    }

    /**
     * Handle remote user eviction
     */
    #handleRemoteEvictUser (message: EvictUser): void {
        this.#assignsCache.delete(message.userId);

        if (this.#presenceEngine) {
            this.#presenceEngine.removePresence(message.userId);
        }

        const unsubscribe = this.#userSubscriptions.get(message.userId);

        if (unsubscribe) {
            unsubscribe();
            this.#userSubscriptions.delete(message.userId);
        }
    }

    /**
     * Broadcast message to other nodes
     */
    async #broadcastToNodes (message: DistributedChannelMessage): Promise<void> {
        if (!this.#backend) {
            return;
        }

        try {
            await this.#backend.broadcast(this.#endpointId, this.#name, message);
        } catch {
            // Silently ignore broadcast errors to prevent cascading failures
        }
    }
}
