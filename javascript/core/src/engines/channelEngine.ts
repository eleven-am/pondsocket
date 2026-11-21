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
    type UserGetRequest,
    type UserGetResponse,
    type UserJoined,
    type UserLeft,
    type UserMessage,
    type UserRemove,
} from '../types';

function mapToObject<T> (map: Map<string, T>): Record<string, T> {
    return Object.fromEntries(map);
}

interface QueuedDelivery {
    event: ChannelEvent;
    membershipToken: symbol;
    bytes: number;
}

interface DeliveryQueue {
    items: QueuedDelivery[];
    bytes: number;
    running: boolean;
}

export class ChannelEngine {
    readonly #endpointId: string;

    readonly #backend: IDistributedBackend | null;

    readonly #nodeId: string;

    #presenceEngine: PresenceEngine | null = null;

    #assignsCache: Map<string, PondAssigns> = new Map();

    #userSubscriptions: Map<string, Unsubscribe> = new Map();

    #membershipTokens: Map<string, symbol> = new Map();

    #deliveryQueues: Map<string, DeliveryQueue> = new Map();

    #messageHandlers: Map<string, (event: ChannelEvent) => void> = new Map();

    #overflowHandlers: Map<string, () => void> = new Map();

    #removedHandlers: Map<string, () => void> = new Map();

    readonly #maxPendingMessagesPerUser: number;

    readonly #maxPendingBytesPerUser: number;

    readonly #deliveryTimeout: number;

    #publisher: Subject<InternalChannelEvent> = new Subject();

    #distributedSubscription: Unsubscribe | null = null;

    #livenessSubscription: Unsubscribe | null = null;

    #nodeUsers: Map<string, Set<string>> = new Map();

    #pendingUserGets: Map<string, { resolve: (data: UserData | null) => void, timer: ReturnType<typeof setTimeout> }> = new Map();

    readonly #name: string;

    #closed: boolean = false;

    #distributedReady: Promise<void> | null = null;

    #distributedFailure: Error | null = null;

    #closeHandlers: Set<(error: Error | null) => void> = new Set();

    constructor (public parent: LobbyEngine, name: string, backend: IDistributedBackend | null = null, maxPendingMessagesPerUser: number = 256, maxPendingBytesPerUser: number = 4 * 1024 * 1024, deliveryTimeout: number = 10_000) {
        this.#name = name;
        this.#backend = backend;
        this.#nodeId = uuid();
        this.#endpointId = parent.parent.path;
        this.#maxPendingMessagesPerUser = maxPendingMessagesPerUser;
        this.#maxPendingBytesPerUser = maxPendingBytesPerUser;
        this.#deliveryTimeout = deliveryTimeout;

        if (this.#backend) {
            this.#livenessSubscription = this.parent.registerNodeLiveness(this.#backend, (nodeId) => {
                this.#handleStaleNode(nodeId);
            });
            this.#distributedReady = this.#setupDistributedSubscription();
            void this.#distributedReady.catch((error) => {
                this.#failDistributed(error);
            });
        }
    }

    get name (): string {
        return this.#name;
    }

    get users (): Set<string> {
        return new Set(this.#assignsCache.keys());
    }

    ready (): Promise<void> {
        return this.#distributedReady ?? Promise.resolve();
    }

    closeIfUnused (): void {
        if (this.#userSubscriptions.size === 0) {
            this.close();
        }
    }

    onClose (handler: (error: Error | null) => void): Unsubscribe {
        if (this.#closed) {
            handler(this.#distributedFailure);

            return () => {};
        }

        this.#closeHandlers.add(handler);

        return () => this.#closeHandlers.delete(handler);
    }

    addUser (userId: string, assigns: PondAssigns, onMessage: (event: ChannelEvent) => void, requestId = uuid(), onOverflow: () => void = () => {}, onRemoved: () => void = () => {}): Unsubscribe {
        this.#ensureOpen();
        if (this.users.has(userId)) {
            const message = `ChannelEngine: User with id ${userId} already exists in channel ${this.name}`;

            throw new HttpError(400, message);
        }

        const isFirstUser = this.users.size === 0;

        onMessage({
            channelName: this.#name,
            requestId,
            action: ServerActions.SYSTEM,
            event: Events.ACKNOWLEDGE,
            payload: {},
        });

        this.#assignsCache.set(userId, assigns);
        this.#membershipTokens.set(userId, Symbol(userId));
        this.#messageHandlers.set(userId, onMessage);
        this.#overflowHandlers.set(userId, onOverflow);
        this.#removedHandlers.set(userId, onRemoved);
        try {
            this.#buildSubscriber(userId, onMessage);
        } catch (error) {
            this.#assignsCache.delete(userId);
            this.#membershipTokens.delete(userId);
            this.#messageHandlers.delete(userId);
            this.#overflowHandlers.delete(userId);
            this.#removedHandlers.delete(userId);
            this.#discardDeliveryQueue(userId);
            throw error;
        }
        if (isFirstUser && this.#backend) {
            this.#afterDistributedReady(() => this.#requestChannelState());
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
            action,
        });
    }

    broadcastMessage (userId: string, message: ClientMessage): Promise<void> {
        if (!this.users.has(userId)) {
            const messageText = `ChannelEngine: User with id ${userId} does not exist in channel ${this.name}`;

            throw new HttpError(404, messageText);
        }

        const responseEvent: BroadcastEvent = {
            ...message,
            sender: userId,
            action: ServerActions.BROADCAST,
        };

        return this.parent.middleware.runAsync(responseEvent, this, (error) => {
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
        const locallyOwned = this.#userSubscriptions.has(userId);
        const onMessage = this.#messageHandlers.get(userId);

        if (onMessage) {
            const event: ChannelEvent = {
                channelName: this.#name,
                requestId: uuid(),
                action: ServerActions.SYSTEM,
                event: 'kicked_out',
                payload: { message: reason,
                    code: 403 },
            };

            try {
                void Promise.resolve(onMessage(event)).catch((error) => {
                    this.parent.parent.reportError(error, `Channel ${this.name} kick delivery failed`);
                });
            } catch (error) {
                this.parent.parent.reportError(error, `Channel ${this.name} kick delivery failed`);
            }
        }

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

        if (locallyOwned) {
            this.removeUser(userId, true);
        }
    }

    requestUserRemoval (userId: string): void {
        this.#broadcastToNodes({
            type: DistributedMessageType.USER_REMOVE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId,
        });
    }

    getUserAcrossNodes (userId: string, timeoutMs = 5_000): Promise<UserData | null> {
        if (!this.#backend || this.#closed) {
            return Promise.resolve(null);
        }

        if (!Number.isFinite(timeoutMs) || timeoutMs <= 0) {
            return Promise.reject(new TypeError('timeoutMs must be greater than zero'));
        }

        const requestId = uuid();

        return new Promise<UserData | null>((resolve) => {
            const timer = setTimeout(() => {
                this.#pendingUserGets.delete(requestId);
                resolve(null);
            }, timeoutMs);

            this.#pendingUserGets.set(requestId, {
                resolve,
                timer,
            });

            this.#broadcastToNodes({
                type: DistributedMessageType.USER_GET_REQUEST,
                endpointName: this.#endpointId,
                channelName: this.#name,
                userId,
                requestId,
                fromNode: this.#nodeId,
            });
        });
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
        const event: ChannelEvent = {
            channelName: this.#name,
            requestId: uuid(),
            action: ServerActions.ERROR,
            event: 'destroyed',
            payload: { message: reason ?? 'Channel has been destroyed' },
        };

        this.#messageHandlers.forEach((onMessage) => {
            try {
                void Promise.resolve(onMessage(event)).catch((error) => {
                    this.parent.parent.reportError(error, `Channel ${this.name} destroy delivery failed`);
                });
            } catch (error) {
                this.parent.parent.reportError(error, `Channel ${this.name} destroy delivery failed`);
            }
        });

        this.#broadcastToNodes({
            type: DistributedMessageType.USER_MESSAGE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            fromUserId: SystemSender.CHANNEL,
            event: event.event,
            payload: event.payload,
            requestId: event.requestId,
            recipientDescriptor: ChannelReceiver.ALL_USERS,
            action: event.action,
        });

        this.close();
    }

    removeUser (userId: string, skipDistributedBroadcast = false): void {
        let userData: UserData;

        try {
            userData = this.getUserData(userId);
        } catch {
            return;
        }

        try {
            this.#cleanupLocalUser(userId);

            if (!skipDistributedBroadcast) {
                this.#broadcastToNodes({
                    type: DistributedMessageType.USER_LEFT,
                    endpointName: this.#endpointId,
                    channelName: this.#name,
                    userId,
                });
            }
        } catch (_) {
            void 0;
        }

        if (this.#userSubscriptions.size === 0) {
            this.close();
        }

        if (this.parent.leaveCallback) {
            try {
                const result = this.parent.leaveCallback({
                    user: userData,
                    channel: this.parent.wrapChannel(this),
                });

                void Promise.resolve(result).catch(() => {});
            } catch (_) {
                void 0;
            }
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
        if (this.#closed) {
            return;
        }

        this.#closed = true;
        this.#userSubscriptions.forEach((unsubscribe) => {
            try {
                unsubscribe();
            } catch (_) {
                void 0;
            }
        });
        this.#userSubscriptions.clear();

        this.#assignsCache.clear();
        this.#membershipTokens.clear();
        this.#messageHandlers.clear();
        this.#overflowHandlers.clear();
        this.#removedHandlers.clear();
        this.#deliveryQueues.forEach((queue) => {
            queue.items.length = 0;
            queue.bytes = 0;
        });
        this.#deliveryQueues.clear();

        if (this.#presenceEngine) {
            this.#presenceEngine.close();
            this.#presenceEngine = null;
        }

        if (this.#distributedSubscription) {
            try {
                this.#distributedSubscription();
            } catch (_) {
                void 0;
            }
            this.#distributedSubscription = null;
        }

        if (this.#livenessSubscription) {
            try {
                this.#livenessSubscription();
            } catch (_) {
                void 0;
            }
            this.#livenessSubscription = null;
        }

        this.#pendingUserGets.forEach((pending) => {
            clearTimeout(pending.timer);
            pending.resolve(null);
        });
        this.#pendingUserGets.clear();

        for (const nodeId of this.#nodeUsers.keys()) {
            if (this.#backend) {
                this.parent.untrackDistributedNode(this.#backend, nodeId);
            }
        }
        this.#nodeUsers.clear();

        this.#publisher.close();

        this.parent.deleteChannel(this.name);

        const closeHandlers = [...this.#closeHandlers];

        this.#closeHandlers.clear();
        closeHandlers.forEach((handler) => {
            try {
                handler(this.#distributedFailure);
            } catch (_) {
                void 0;
            }
        });
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

    #cleanupLocalUser (userId: string): void {
        const onRemoved = this.#removedHandlers.get(userId);

        this.#assignsCache.delete(userId);
        this.#membershipTokens.delete(userId);
        this.#messageHandlers.delete(userId);
        this.#overflowHandlers.delete(userId);
        this.#removedHandlers.delete(userId);
        this.#discardDeliveryQueue(userId);
        this.#safeRemovePresence(userId);

        const unsubscribe = this.#userSubscriptions.get(userId);

        this.#userSubscriptions.delete(userId);
        if (unsubscribe) {
            try {
                unsubscribe();
            } catch (_) {
                void 0;
            }
        }
        try {
            onRemoved?.();
        } catch (_) {
            void 0;
        }
    }

    #buildSubscriber (userId: string, onMessage: (event: ChannelEvent) => void): void {
        const membershipToken = this.#membershipTokens.get(userId);

        if (!membershipToken) {
            throw new Error(`Channel membership for ${userId} was not initialized`);
        }

        const subscription = this.#publisher.subscribe(({ recipients, ...event }) => {
            if (this.#membershipTokens.get(userId) !== membershipToken || !recipients.includes(userId)) {
                return;
            }

            let bytes: number;

            try {
                bytes = Buffer.byteLength(JSON.stringify(event));
            } catch (_) {
                bytes = this.#maxPendingBytesPerUser + 1;
            }

            let queue = this.#deliveryQueues.get(userId);

            if (!queue) {
                queue = { items: [],
                    bytes: 0,
                    running: false };
                this.#deliveryQueues.set(userId, queue);
            }

            if (queue.items.length >= this.#maxPendingMessagesPerUser || queue.bytes + bytes > this.#maxPendingBytesPerUser) {
                const error = new Error(`Channel delivery backlog exceeded for user ${userId}`);

                this.parent.parent.reportError(error, `Channel ${this.name} overflow`);
                const overflow = this.#overflowHandlers.get(userId);

                this.removeUser(userId);
                overflow?.();

                return;
            }

            queue.items.push({ event,
                membershipToken,
                bytes });
            queue.bytes += bytes;
            if (!queue.running) {
                queue.running = true;
                void this.#drainDeliveryQueue(userId, queue, onMessage);
            }
        });

        this.#userSubscriptions.set(userId, subscription);
    }

    async #drainDeliveryQueue (userId: string, queue: DeliveryQueue, onMessage: (event: ChannelEvent) => void): Promise<void> {
        try {
            while (queue.items.length > 0) {
                const item = queue.items.shift()!;

                queue.bytes -= item.bytes;
                if (this.#membershipTokens.get(userId) !== item.membershipToken) {
                    continue;
                }

                let timeout: ReturnType<typeof setTimeout> | null = null;

                try {
                    const delivery = (async () => {
                        if (item.event.action === ServerActions.PRESENCE) {
                            await Promise.resolve(onMessage(item.event));

                            return;
                        }

                        const event = await this.parent.processOutgoingEvents(item.event, this, userId);

                        if (event && this.#membershipTokens.get(userId) === item.membershipToken) {
                            await Promise.resolve(onMessage(event));
                        }
                    })();
                    const deadline = new Promise<boolean>((resolve) => {
                        timeout = setTimeout(() => resolve(true), this.#deliveryTimeout);
                        timeout.unref?.();
                    });
                    const timedOut = await Promise.race([delivery.then(() => false), deadline]);

                    if (timedOut) {
                        const error = new Error(`Channel delivery timed out for user ${userId}`);
                        const overflow = this.#overflowHandlers.get(userId);

                        this.parent.parent.reportError(error, `Channel ${this.name} delivery timeout`);
                        this.removeUser(userId);
                        overflow?.();
                        break;
                    }
                } catch (error) {
                    this.parent.parent.reportError(error, `Channel ${this.name} delivery failed`);
                } finally {
                    if (timeout) {
                        clearTimeout(timeout);
                    }
                }
            }
        } finally {
            queue.running = false;
            if (this.#deliveryQueues.get(userId) === queue && queue.items.length === 0) {
                this.#deliveryQueues.delete(userId);
            }
        }
    }

    #discardDeliveryQueue (userId: string): void {
        const queue = this.#deliveryQueues.get(userId);

        if (!queue) {
            return;
        }
        queue.items.length = 0;
        queue.bytes = 0;
        this.#deliveryQueues.delete(userId);
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

        const unsubscribe = await this.#backend.subscribeToChannel(this.#endpointId, this.#name, (message) => {
            this.#handleDistributedMessage(message);
        });

        if (this.#closed) {
            unsubscribe();

            return;
        }

        this.#distributedSubscription = unsubscribe;
    }

    #afterDistributedReady (callback: () => void): void {
        if (!this.#distributedReady) {
            callback();

            return;
        }

        this.#distributedReady.then(() => {
            if (!this.#closed) {
                return callback();
            }
        }).catch(() => {});
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
            case DistributedMessageType.USER_REMOVE:
                this.#handleRemoteUserRemove(message);
                break;
            case DistributedMessageType.USER_GET_REQUEST:
                this.#handleRemoteUserGetRequest(message);
                break;
            case DistributedMessageType.USER_GET_RESPONSE:
                this.#handleRemoteUserGetResponse(message);
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
        if (this.#userSubscriptions.size === 0) {
            return;
        }

        const users = Array.from(this.#userSubscriptions.keys()).map((id) => ({
            id,
            assigns: this.#assignsCache.get(id) || {},
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
        this.#closeIfEmpty();
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

        if (message.action === ServerActions.ERROR && message.event === 'destroyed') {
            recipients.forEach((userId) => {
                const onMessage = this.#messageHandlers.get(userId);

                if (onMessage) {
                    try {
                        void Promise.resolve(onMessage({
                            channelName: this.#name,
                            requestId: message.requestId,
                            action: ServerActions.ERROR,
                            event: message.event,
                            payload: message.payload,
                        })).catch((error) => this.parent.parent.reportError(error, `Channel ${this.name} remote destroy delivery failed`));
                    } catch (error) {
                        this.parent.parent.reportError(error, `Channel ${this.name} remote destroy delivery failed`);
                    }
                }
            });

            return;
        }

        const action = message.action === ServerActions.PRESENCE
            ? ServerActions.BROADCAST
            : message.action ?? ServerActions.BROADCAST;
        const internalEvent: InternalChannelEvent = {
            channelName: this.#name,
            requestId: message.requestId,
            action,
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
        const current = this.#assignsCache.get(message.userId);

        if (!current) {
            return;
        }

        if (message.assigns) {
            this.#assignsCache.set(message.userId, message.assigns);

            return;
        }

        if (message.key) {
            this.#assignsCache.set(message.userId, {
                ...current,
                [message.key]: message.value,
            });
        }
    }

    #handleRemoteEvictUser (message: EvictUser): void {
        const locallyOwned = this.#userSubscriptions.has(message.userId);
        const onMessage = this.#messageHandlers.get(message.userId);

        if (onMessage) {
            const kickedOutEvent: ChannelEvent = {
                channelName: this.#name,
                requestId: uuid(),
                action: ServerActions.SYSTEM,
                event: 'kicked_out',
                payload: {
                    message: message.reason,
                    code: 403,
                },
            };

            try {
                void Promise.resolve(onMessage(kickedOutEvent)).catch((error) => {
                    this.parent.parent.reportError(error, `Channel ${this.name} eviction delivery failed`);
                });
            } catch (error) {
                this.parent.parent.reportError(error, `Channel ${this.name} eviction delivery failed`);
            }
        }

        if (locallyOwned) {
            this.removeUser(message.userId);

            return;
        }

        this.#assignsCache.delete(message.userId);
        this.#safeRemovePresence(message.userId);
        this.#untrackNodeUser(message.sourceNodeId, message.userId);
        this.#closeIfEmpty();
    }

    #handleRemoteUserRemove (message: UserRemove): void {
        if (this.#userSubscriptions.has(message.userId)) {
            this.removeUser(message.userId);

            return;
        }

        this.#assignsCache.delete(message.userId);
        this.#safeRemovePresence(message.userId);
        this.#untrackNodeUser(message.sourceNodeId, message.userId);
        this.#closeIfEmpty();
    }

    #handleRemoteUserGetRequest (message: UserGetRequest): void {
        if (!this.#userSubscriptions.has(message.userId)) {
            return;
        }

        this.#broadcastToNodes({
            type: DistributedMessageType.USER_GET_RESPONSE,
            endpointName: this.#endpointId,
            channelName: this.#name,
            userId: message.userId,
            requestId: message.requestId,
            assigns: this.#assignsCache.get(message.userId) || {},
            presence: this.#presenceEngine?.getPresence(message.userId) || {},
        });
    }

    #handleRemoteUserGetResponse (message: UserGetResponse): void {
        const pending = this.#pendingUserGets.get(message.requestId);

        if (!pending) {
            return;
        }

        clearTimeout(pending.timer);
        this.#pendingUserGets.delete(message.requestId);

        pending.resolve({
            id: message.userId,
            assigns: message.assigns || {},
            presence: message.presence || {},
        });
    }

    #handleStaleNode (nodeId: string): void {
        const users = this.#nodeUsers.get(nodeId);

        if (!users) {
            return;
        }

        for (const userId of users) {
            this.#assignsCache.delete(userId);
            this.#safeRemovePresence(userId);
        }
        this.#nodeUsers.delete(nodeId);
        this.#closeIfEmpty();
    }

    #trackNodeUser (sourceNodeId: string | undefined, userId: string): void {
        if (!sourceNodeId || !this.#backend) {
            return;
        }

        let users = this.#nodeUsers.get(sourceNodeId);

        if (!users) {
            users = new Set();
            this.#nodeUsers.set(sourceNodeId, users);
            this.parent.trackDistributedNode(this.#backend, sourceNodeId);
        }

        users.add(userId);
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
                if (this.#backend) {
                    this.parent.untrackDistributedNode(this.#backend, sourceNodeId);
                }
            }
        }
    }

    async #broadcastToNodes (message: DistributedChannelMessage): Promise<void> {
        if (!this.#backend) {
            return;
        }

        try {
            if (this.#closed) {
                return;
            }
            await this.#backend.broadcast(this.#endpointId, this.#name, message);
        } catch (error) {
            this.#failDistributed(error);
        }
    }

    #closeIfEmpty (): void {
        if (this.#assignsCache.size === 0) {
            this.close();
        }
    }

    #failDistributed (error: unknown): void {
        if (this.#closed) {
            return;
        }

        this.#distributedFailure = error instanceof Error ? error : new Error(String(error));
        this.parent.parent.reportError(this.#distributedFailure, `Distributed channel ${this.name} failed`);
        this.close();
    }

    #ensureOpen (): void {
        if (this.#distributedFailure) {
            throw new HttpError(503, `Distributed channel ${this.name} is unavailable: ${this.#distributedFailure.message}`);
        }
        if (this.#closed) {
            throw new HttpError(410, `Channel ${this.name} is closed`);
        }
    }
}
