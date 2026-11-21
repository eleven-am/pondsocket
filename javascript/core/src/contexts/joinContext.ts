import {
    ChannelEvent,
    ChannelReceivers,
    AnyPondSchema,
    AssignsOf,
    ErrorTypes,
    EventsOf,
    JoinParamsOf,
    PondMessage,
    PondObject,
    PresenceOf,
    ServerActions,
    UserData,
} from '@eleven-am/pondsocket-common';

import { BaseContext } from './baseContext';
import { JoinRequestOptions, RequestCache } from '../abstracts/types';
import { ChannelEngine } from '../engines/channelEngine';
import { HttpError } from '../errors/httpError';

export class JoinContext<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> extends BaseContext<Path, Schema, Extract<keyof EventsOf<Schema>, string>, JoinParamsOf<Schema>> {
    readonly #options: JoinRequestOptions<Path, Schema>;

    readonly #user: RequestCache;

    #newAssigns: AssignsOf<Schema>;

    #executed: boolean;

    #accepted: boolean;

    #cancelled: boolean;

    constructor (options: JoinRequestOptions<Path, Schema>, engine: ChannelEngine, user: RequestCache) {
        super(engine, options.params, engine.name, options.joinParams, user.clientId);
        this.#options = options;
        this.#user = user;
        this.#executed = false;
        this.#accepted = false;
        this.#cancelled = false;
        this.#newAssigns = { ...user.assigns } as AssignsOf<Schema>;
    }

    get user (): UserData<PresenceOf<Schema>, AssignsOf<Schema>> {
        return {
            id: this.#options.clientId,
            assigns: this.#options.assigns,
            presence: {} as PresenceOf<Schema>,
        };
    }

    get joinParams (): JoinParamsOf<Schema> {
        return this.#options.joinParams;
    }

    get hasResponded (): boolean {
        return this.#executed;
    }

    cancel (): void {
        this.#cancelled = true;
        this.#executed = true;
    }

    accept (assigns?: Partial<AssignsOf<Schema>>): JoinContext<Path, Schema> {
        if (assigns) {
            this.assign(assigns);
        }
        this.#performChecks();
        const onMessage = this.engine.parent.parent.sendMessage.bind(this.engine.parent.parent, this.#user.socket);
        let active = true;
        let unsubscribeClose = () => {};
        let wrappedSubscription = () => {};
        const cleanupRegistration = () => {
            if (!active) {
                return;
            }
            active = false;
            unsubscribeClose();
            this.#user.subscriptions.delete(wrappedSubscription);
            this.#user.channelSubscriptions?.delete(this.engine.name);
        };

        const subscription = this.engine.addUser(
            this.#user.clientId,
            this.#newAssigns,
            onMessage,
            this.#user.requestId,
            () => this.#user.socket.close(1009, 'Channel delivery backlog exceeded'),
            cleanupRegistration,
        );
        wrappedSubscription = () => {
            if (!active) {
                return;
            }
            cleanupRegistration();
            subscription();
        };

        this.#user.subscriptions.add(wrappedSubscription);
        this.#user.channelSubscriptions?.set(this.engine.name, wrappedSubscription);
        unsubscribeClose = this.engine.onClose((error) => {
            if (!active) {
                return;
            }

            wrappedSubscription();
            if (!error) {
                return;
            }
            const message = 'Distributed channel unavailable';

            this.#directMessage({
                event: ErrorTypes.CHANNEL_ERROR,
                action: ServerActions.ERROR,
                channelName: this.engine.name,
                requestId: this.#user.requestId,
                payload: {
                    code: ErrorTypes.CHANNEL_ERROR,
                    message,
                    status: 503,
                    statusCode: 503,
                    error: {
                        message,
                        status: 503,
                    },
                },
            });
            this.#user.socket.close(1012, message);
        });
        this.#accepted = true;

        return this;
    }

    decline (message?: string, errorCode?: number): JoinContext<Path, Schema> {
        this.#performChecks();

        const errorMessage: ChannelEvent = {
            event: ErrorTypes.UNAUTHORIZED_JOIN_REQUEST,
            payload: {
                code: ErrorTypes.UNAUTHORIZED_JOIN_REQUEST,
                message: message || 'Unauthorized connection',
                status: errorCode || 401,
                statusCode: errorCode || 401,
                error: {
                    message: message || 'Unauthorized connection',
                    status: errorCode || 401,
                },
            },
            channelName: this.engine.name,
            action: ServerActions.ERROR,
            requestId: this.#user.requestId,
        };

        this.#directMessage(errorMessage);

        return this;
    }

    assign (assigns: Partial<AssignsOf<Schema>>): JoinContext<Path, Schema> {
        this.#ensureActive();
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

    reply (event: string, payload: PondMessage): JoinContext<Path, Schema> {
        this.#ensureActive();
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

    trackPresence (presence: PresenceOf<Schema>): JoinContext<Path, Schema> {
        this.#ensureActive();
        this.engine.trackPresence(this.#user.clientId, presence);

        return this;
    }

    protected _sendMessageToRecipients (recipient: ChannelReceivers, event: string, payload: PondObject) {
        this.#ensureActive();
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
        this.#ensureActive();
        if (this.#executed) {
            const message = `Request to join channel ${this.engine.name} rejected: Request already executed`;

            throw new HttpError(403, message);
        }

        this.#executed = true;
    }

    #ensureActive (): void {
        if (this.#cancelled) {
            throw new HttpError(410, `Request to join channel ${this.engine.name} was cancelled`);
        }
    }
}
