import {
    ChannelEvent,
    ChannelReceiver,
    ChannelReceivers,
    ErrorTypes,
    JoinParams,
    PondAssigns,
    PondMessage,
    PondObject,
    PondPresence,
    ServerActions,
    UserData,
} from '@eleven-am/pondsocket-common';

import { BaseContext } from './baseContext';
import { JoinRequestOptions, RequestCache } from '../abstracts/types';
import { ChannelEngine } from '../engines/channelEngine';
import { HttpError } from '../errors/httpError';


/**
 * JoinContext combines the functionality of JoinRequest and JoinResponse
 * to provide a unified interface for handling join events in a channel.
 */
export class JoinContext<Path extends string> extends BaseContext<Path> {
    readonly #options: JoinRequestOptions<Path>;

    readonly #user: RequestCache;

    #newAssigns: PondAssigns;

    #executed: boolean;

    #accepted: boolean;

    constructor (options: JoinRequestOptions<Path>, engine: ChannelEngine, user: RequestCache) {
        super(engine, options.params, engine.name, options.joinParams, user.clientId);
        this.#options = options;
        this.#user = user;
        this.#executed = false;
        this.#accepted = false;
        this.#newAssigns = { ...user.assigns };
    }

    /**
     * The user who sent the request
     */
    get user (): UserData {
        return {
            id: this.#options.clientId,
            assigns: this.#options.assigns,
            presence: {},
        };
    }

    /**
     * The join parameters
     */
    get joinParams (): JoinParams {
        return this.#options.joinParams;
    }

    /**
     * Whether the request has been handled
     */
    get hasResponded (): boolean {
        return this.#executed;
    }

    /**
     * Accepts the join request
     */
    accept (): JoinContext<Path> {
        this.#performChecks();
        const onMessage = this.engine.parent.parent.sendMessage.bind(this.engine.parent.parent, this.#user.socket);

        const subscription = this.engine.addUser(this.#user.clientId, this.#newAssigns, onMessage);

        this.#user.subscriptions.add(subscription);
        this.#accepted = true;

        return this;
    }

    /**
     * Declines the join request
     */
    decline (message?: string, errorCode?: number): JoinContext<Path> {
        this.#performChecks();

        const errorMessage: ChannelEvent = {
            event: ErrorTypes.UNAUTHORIZED_JOIN_REQUEST,
            payload: {
                message: message || 'Unauthorized connection',
                code: errorCode || 401,
            },
            channelName: this.engine.name,
            action: ServerActions.ERROR,
            requestId: this.#user.requestId,
        };

        this.#directMessage(errorMessage);

        return this;
    }

    /**
     * Assigns data to the user
     */
    assign (assigns: PondAssigns): JoinContext<Path> {
        if (this.#accepted) {
            this.engine.updateAssigns(this.#user.clientId, assigns);
        } else {
            this.#newAssigns = {
                ...this.#newAssigns,
                ...assigns,
            };
        }

        return this;
    }

    /**
     * Sends a direct reply to the user
     */
    reply (event: string, payload: PondMessage): JoinContext<Path> {
        const message: ChannelEvent = {
            action: ServerActions.SYSTEM,
            channelName: this.engine.name,
            requestId: this.#user.requestId,
            payload,
            event,
        };

        this.#directMessage(message);

        return this;
    }

    /**
     * Broadcasts a message to specific users
     */
    broadcastTo (event: string, payload: PondMessage, userIds: string | string[]): JoinContext<Path> {
        const ids = Array.isArray(userIds) ? userIds : [userIds];

        this.#sendMessage(ids, event, payload);

        return this;
    }

    /**
     * Broadcasts a message to all users in the channel
     */
    broadcast (event: string, payload: PondMessage): JoinContext<Path> {
        this.#sendMessage(ChannelReceiver.ALL_USERS, event, payload);

        return this;
    }

    /**
     * Broadcasts a message to all users except the sender
     */
    broadcastFrom (event: string, payload: PondMessage): JoinContext<Path> {
        this.#sendMessage(ChannelReceiver.ALL_EXCEPT_SENDER, event, payload);

        return this;
    }

    /**
     * Tracks the user's presence
     */
    trackPresence (presence: PondPresence): JoinContext<Path> {
        this.engine.trackPresence(this.#user.clientId, presence);

        return this;
    }

    /**
     * Sends a message to specific recipients
     */
    #sendMessage (recipient: ChannelReceivers, event: string, payload: PondObject) {
        this.engine.sendMessage(
            this.#user.clientId,
            recipient,
            ServerActions.BROADCAST,
            event,
            payload,
            this.#user.requestId,
        );
    }

    /**
     * Sends a direct message to the client
     */
    #directMessage (event: ChannelEvent) {
        this.engine.parent.parent.sendMessage(this.#user.socket, event);
    }

    /**
     * Performs checks before handling the request
     */
    #performChecks (): void {
        if (this.#executed) {
            const message = `Request to join channel ${this.engine.name} rejected: Request already executed`;

            throw new HttpError(403, message);
        }

        this.#executed = true;
    }
}
