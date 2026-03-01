import {
    ChannelEvent,
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

    get user (): UserData {
        return {
            id: this.#options.clientId,
            assigns: this.#options.assigns,
            presence: {},
        };
    }

    get joinParams (): JoinParams {
        return this.#options.joinParams;
    }

    get hasResponded (): boolean {
        return this.#executed;
    }

    accept (): JoinContext<Path> {
        this.#performChecks();
        const onMessage = this.engine.parent.parent.sendMessage.bind(this.engine.parent.parent, this.#user.socket);

        const subscription = this.engine.addUser(this.#user.clientId, this.#newAssigns, onMessage);

        this.#user.subscriptions.add(subscription);
        this.#accepted = true;

        return this;
    }

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

    trackPresence (presence: PondPresence): JoinContext<Path> {
        this.engine.trackPresence(this.#user.clientId, presence);

        return this;
    }

    protected _sendMessageToRecipients (recipient: ChannelReceivers, event: string, payload: PondObject) {
        this.engine.sendMessage(
            this.#user.clientId,
            recipient,
            ServerActions.BROADCAST,
            event,
            payload,
            this.#user.requestId,
        );
    }

    #directMessage (event: ChannelEvent) {
        this.engine.parent.parent.sendMessage(this.#user.socket, event);
    }

    #performChecks (): void {
        if (this.#executed) {
            const message = `Request to join channel ${this.engine.name} rejected: Request already executed`;

            throw new HttpError(403, message);
        }

        this.#executed = true;
    }
}
