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
import { type BroadcastEvent, type ChannelSenders, type InternalChannelEvent } from '../abstracts/types';
import { HttpError } from '../errors/httpError';
import {
    DistributedMessageType,
    type AssignsUpdate,
    type DistributedChannelMessage,
    type EvictUser,
    type IDistributedBackend,
    type PresenceRemoved,
    type PresenceUpdate,
    type StateRequest,
    type StateResponse,
    type UserJoined,
    type UserLeft,
    type UserMessage,
} from '../types';

function mapToObject<T> (map: Map<string, T>): Record<string, T> {
    const result: Record<string, T> = {};

    for (const [key, value] of map) {
        result[key] = value;
    }

    return result;
}

export class ChannelEngine {
    readonly #endpointId: string;

    readonly #backend: IDistributedBackend | null;

    readonly #nodeId: string;

    #presenceEngine: PresenceEngine | null = null;

    #assignsCache: Map<string, PondAssigns> = new Map();

    #userSubscriptions: Map<string, Unsubscribe> = new Map();

    #publisher: Subject<InternalChannelEvent> = new Subject();

    #distributedSubscription: Unsubscribe | null = null;

    #heartbeatSubscription: Unsubscribe | null = null;

    #nodeLastSeen: Map<string, number> = new Map();

    #nodeUsers: Map<string, Set<string>> = new Map();

    #staleNodeTimer: ReturnType<typeof setInterval> | null = null;

    readonly #name: string;

    constructor (public parent: LobbyEngine, name: string, backend: IDistributedBackend | null = null) {
        this.#name = name;
        this.#backend = backend;
        this.#nodeId = uuid();
        this.#endpointId = parent.parent.path;

        if (this.#backend) {
            this.#setupDistributedSubscription().catch(() => {});
            this.#setupHeartbeatTracking();
        }
    }

    get name (): string {
        return this.#name;
    }

    get users (): Set<string> {
        return new Set(this.#assignsCache.keys());
    }

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

        this.#broadcastToNodes({
            type: DistributedMessageType.USER_JOINED,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId,
            presence: {},
            assigns,
        });

        return () => this.removeUser(userId);
    }

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

        this.#broadcastToNodes({
            type: DistributedMessageType.USER_MESSAGE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            fromUserId: sender as string,
            event,
            payload,
            requestId,
            recipientDescriptor: recipient,
        });
    }

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

    trackPresence (userId: string, presence: PondPresence): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.trackPresence(userId, presence);

        this.#broadcastToNodes({
            type: DistributedMessageType.PRESENCE_UPDATE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId,
            presence,
        });
    }

    updatePresence (userId: string, presence: PondPresence): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.updatePresence(userId, presence);

        this.#broadcastToNodes({
            type: DistributedMessageType.PRESENCE_UPDATE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId,
            presence,
        });
    }

    removePresence (userId: string): void {
        if (this.#presenceEngine) {
            this.#presenceEngine.removePresence(userId);

            this.#broadcastToNodes({
                type: DistributedMessageType.PRESENCE_REMOVED,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
            });
        }
    }

    upsertPresence (userId: string, presence: PondPresence): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.upsertPresence(userId, presence);

        this.#broadcastToNodes({
            type: DistributedMessageType.PRESENCE_UPDATE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId,
            presence,
        });
    }

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

        this.#broadcastToNodes({
            type: DistributedMessageType.ASSIGNS_UPDATE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId,
            assigns: newAssigns,
        });
    }

    kickUser (userId: string, reason: string): void {
        this.sendMessage(SystemSender.CHANNEL, [userId], ServerActions.SYSTEM, 'kicked_out', {
            message: reason,
            code: 403,
        });

        this.sendMessage(SystemSender.CHANNEL, ChannelReceiver.ALL_USERS, ServerActions.SYSTEM, 'kicked', {
            userId,
            reason,
        });

        this.#broadcastToNodes({
            type: DistributedMessageType.EVICT_USER,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId,
            reason,
        });

        this.removeUser(userId, true);
    }

    getAssigns (): UserAssigns {
        return mapToObject(this.#assignsCache);
    }

    getPresence (): UserPresences {
        if (!this.#presenceEngine) {
            return {};
        }

        return mapToObject(this.#presenceEngine.getAllPresence());
    }

    destroy (reason?: string): void {
        this.sendMessage(SystemSender.CHANNEL, ChannelReceiver.ALL_USERS, ServerActions.ERROR, 'destroyed', {
            message: reason ?? 'Channel has been destroyed',
        });

        this.close();
    }

    removeUser (userId: string, skipDistributedBroadcast = false): void {
        try {
            const userData = this.getUserData(userId);
            const unsubscribe = this.#userSubscriptions.get(userId);

            this.#assignsCache.delete(userId);
            this.#safeRemovePresence(userId);

            if (unsubscribe) {
                unsubscribe();
                this.#userSubscriptions.delete(userId);
            }

            if (!skipDistributedBroadcast) {
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
        } catch (_) {
            void 0;
        }
    }

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

        if (this.#heartbeatSubscription) {
            this.#heartbeatSubscription();
            this.#heartbeatSubscription = null;
        }

        if (this.#staleNodeTimer) {
            clearInterval(this.#staleNodeTimer);
            this.#staleNodeTimer = null;
        }

        this.#nodeLastSeen.clear();
        this.#nodeUsers.clear();

        this.#publisher.close();

        this.parent.deleteChannel(this.name);
    }

    #safeRemovePresence (userId: string): void {
        if (this.#presenceEngine) {
            try {
                this.#presenceEngine.removePresence(userId);
            } catch (_) {
                void 0;
            }
        }
    }

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

    #getOrCreatePresenceEngine (): PresenceEngine {
        if (!this.#presenceEngine) {
            this.#presenceEngine = new PresenceEngine(this.name, (event) => {
                this.#publisher.publish(event);
            });
        }

        return this.#presenceEngine;
    }

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

    async #setupDistributedSubscription (): Promise<void> {
        if (!this.#backend) {
            return;
        }

        this.#distributedSubscription = await this.#backend.subscribeToChannel(this.#endpointId, this.#name, (message) => {
            this.#handleDistributedMessage(message);
        });
    }

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
            case DistributedMessageType.EVICT_USER:
                this.#handleRemoteEvictUser(message);
                break;
            case DistributedMessageType.NODE_HEARTBEAT:
                break;
            default:
                break;
        }
    }

    #requestChannelState (): void {
        this.#broadcastToNodes({
            type: DistributedMessageType.STATE_REQUEST,
            endpointName: this.#endpointId,
            channelName: this.#name,
            fromNode: this.#nodeId,
        });
    }

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

    #handleStateResponse (message: StateResponse): void {
        if (!message.users) {
            return;
        }

        message.users.forEach((user) => {
            if (!this.users.has(user.id)) {
                this.#assignsCache.set(user.id, user.assigns);
                this.#trackNodeUser(message.sourceNodeId, user.id);

                if (user.presence && Object.keys(user.presence).length > 0) {
                    const presenceEngine = this.#getOrCreatePresenceEngine();

                    presenceEngine.upsertPresence(user.id, user.presence);
                }
            }
        });
    }

    #handleRemoteUserJoined (message: UserJoined): void {
        if (this.users.has(message.userId)) {
            return;
        }

        this.#assignsCache.set(message.userId, message.assigns);
        this.#trackNodeUser(message.sourceNodeId, message.userId);

        if (message.presence && Object.keys(message.presence).length > 0) {
            const presenceEngine = this.#getOrCreatePresenceEngine();

            presenceEngine.trackPresence(message.userId, message.presence);
        }
    }

    #handleRemoteUserLeft (message: UserLeft): void {
        this.#assignsCache.delete(message.userId);
        this.#untrackNodeUser(message.sourceNodeId, message.userId);
        this.#safeRemovePresence(message.userId);
    }

    #handleRemoteMessage (message: UserMessage): void {
        const localUsers = Array.from(this.users);
        let recipients: string[];

        if (message.recipientDescriptor === ChannelReceiver.ALL_USERS) {
            recipients = localUsers;
        } else if (message.recipientDescriptor === ChannelReceiver.ALL_EXCEPT_SENDER) {
            recipients = localUsers.filter((u) => u !== message.fromUserId);
        } else if (Array.isArray(message.recipientDescriptor)) {
            recipients = localUsers.filter((u) => (message.recipientDescriptor as string[]).includes(u));
        } else {
            return;
        }

        if (recipients.length === 0) {
            return;
        }

        const internalEvent: InternalChannelEvent = {
            channelName: this.#name,
            requestId: message.requestId,
            action: ServerActions.BROADCAST,
            event: message.event,
            payload: message.payload,
            recipients,
        };

        this.#publisher.publish(internalEvent);
    }

    #handleRemotePresenceUpdate (message: PresenceUpdate): void {
        const presenceEngine = this.#getOrCreatePresenceEngine();

        presenceEngine.upsertPresence(message.userId, message.presence);
    }

    #handleRemotePresenceRemoved (message: PresenceRemoved): void {
        this.#safeRemovePresence(message.userId);
    }

    #handleRemoteAssignsUpdate (message: AssignsUpdate): void {
        this.#assignsCache.set(message.userId, message.assigns);
    }

    #handleRemoteEvictUser (message: EvictUser): void {
        if (this.#userSubscriptions.has(message.userId)) {
            const kickedOutEvent: InternalChannelEvent = {
                channelName: this.#name,
                requestId: uuid(),
                action: ServerActions.SYSTEM,
                event: 'kicked_out',
                payload: {
                    message: message.reason,
                    code: 403,
                },
                recipients: [message.userId],
            };

            this.#publisher.publish(kickedOutEvent);
        }

        this.#assignsCache.delete(message.userId);
        this.#safeRemovePresence(message.userId);

        const unsubscribe = this.#userSubscriptions.get(message.userId);

        if (unsubscribe) {
            unsubscribe();
            this.#userSubscriptions.delete(message.userId);
        }
    }

    #setupHeartbeatTracking (): void {
        if (!this.#backend) {
            return;
        }

        this.#heartbeatSubscription = this.#backend.subscribeToHeartbeats((nodeId) => {
            this.#nodeLastSeen.set(nodeId, Date.now());
        });

        this.#staleNodeTimer = setInterval(() => {
            this.#cleanupStaleNodes();
        }, this.#backend.heartbeatTimeoutMs);
    }

    #cleanupStaleNodes (): void {
        if (!this.#backend) {
            return;
        }

        const now = Date.now();
        const timeout = this.#backend.heartbeatTimeoutMs;

        for (const [nodeId, lastSeen] of this.#nodeLastSeen) {
            if (now - lastSeen >= timeout) {
                const users = this.#nodeUsers.get(nodeId);

                if (users) {
                    for (const userId of users) {
                        this.#assignsCache.delete(userId);
                        this.#safeRemovePresence(userId);
                    }
                    this.#nodeUsers.delete(nodeId);
                }

                this.#nodeLastSeen.delete(nodeId);
            }
        }
    }

    #trackNodeUser (sourceNodeId: string | undefined, userId: string): void {
        if (!sourceNodeId) {
            return;
        }

        let users = this.#nodeUsers.get(sourceNodeId);

        if (!users) {
            users = new Set();
            this.#nodeUsers.set(sourceNodeId, users);
        }

        users.add(userId);

        if (!this.#nodeLastSeen.has(sourceNodeId)) {
            this.#nodeLastSeen.set(sourceNodeId, Date.now());
        }
    }

    #untrackNodeUser (sourceNodeId: string | undefined, userId: string): void {
        if (!sourceNodeId) {
            return;
        }

        const users = this.#nodeUsers.get(sourceNodeId);

        if (users) {
            users.delete(userId);
            if (users.size === 0) {
                this.#nodeUsers.delete(sourceNodeId);
            }
        }
    }

    async #broadcastToNodes (message: DistributedChannelMessage): Promise<void> {
        if (!this.#backend) {
            return;
        }

        try {
            await this.#backend.broadcast(this.#endpointId, this.#name, message);
        } catch (_) {
            void 0;
        }
    }
}
