import {
    BehaviorSubject,
    AnyPondChannelDefinition,
    AnyPondSchema,
    buildPondRoute,
    ChannelEvent,
    channelEventSchema,
    Events,
    JoinParamsOf,
    isPondChannelDefinition,
    Params,
    PathOf,
    PondErrorPayload,
    SchemaOf,
    ServerActions,
    Subject,
    ChannelState,
    Unsubscribe,
} from '@eleven-am/pondsocket-common';

import { Channel } from './channel';
import { ClientMessage, ClientOptions, ConnectionState } from '../types';

const DEFAULT_CONNECTION_TIMEOUT = 10000;
const DEFAULT_JOIN_TIMEOUT = 10000;
const DEFAULT_MAX_RECONNECT_DELAY = 30000;

type DefinedJoinOptions<Definition extends AnyPondChannelDefinition> =
    Record<never, never> extends JoinParamsOf<SchemaOf<Definition>>
        ? { joinParams?: JoinParamsOf<SchemaOf<Definition>> }
        : { joinParams: JoinParamsOf<SchemaOf<Definition>> };

type DefinedRouteOptions<Definition extends AnyPondChannelDefinition> =
    keyof Params<PathOf<Definition>> extends never
        ? { params?: Params<PathOf<Definition>> }
        : { params: Params<PathOf<Definition>> };

export type DefinedChannelOptions<Definition extends AnyPondChannelDefinition> =
    DefinedJoinOptions<Definition> & DefinedRouteOptions<Definition> & { joinTimeout?: number };

type DefinedChannelArguments<Definition extends AnyPondChannelDefinition> =
    Record<never, never> extends DefinedChannelOptions<Definition>
        ? [options?: DefinedChannelOptions<Definition>]
        : [options: DefinedChannelOptions<Definition>];

export abstract class BaseClient {
    protected readonly _address: URL;

    protected readonly _options: Required<Omit<ClientOptions, 'pingInterval'>> & Pick<ClientOptions, 'pingInterval'>;

    protected _disconnecting: boolean;

    protected _reconnectAttempts: number;

    protected _connectionTimeoutId: ReturnType<typeof setTimeout> | undefined;

    protected _reconnectTimeoutId: ReturnType<typeof setTimeout> | undefined;

    protected readonly _broadcaster: Subject<ChannelEvent>;

    protected readonly _connectionState: BehaviorSubject<ConnectionState>;

    protected readonly _errorSubject: Subject<Error>;

    readonly #channels: Map<string, Channel>;

    constructor (endpoint: string, params: Record<string, any> = {}, options: ClientOptions = {}) {
        this._address = this._resolveAddress(endpoint, params);
        this._disconnecting = false;
        this._reconnectAttempts = 0;
        this._options = {
            connectionTimeout: options.connectionTimeout ?? DEFAULT_CONNECTION_TIMEOUT,
            joinTimeout: options.joinTimeout ?? DEFAULT_JOIN_TIMEOUT,
            maxReconnectDelay: options.maxReconnectDelay ?? DEFAULT_MAX_RECONNECT_DELAY,
            pingInterval: options.pingInterval,
        };
        this.#channels = new Map();
        this._broadcaster = new Subject<ChannelEvent>();
        this._connectionState = new BehaviorSubject<ConnectionState>(ConnectionState.DISCONNECTED);
        this._errorSubject = new Subject<Error>();
        this.#init();
    }

    protected abstract _resolveAddress (endpoint: string, params: Record<string, any>): URL;

    public abstract connect (): void;

    protected _clearConnectionTimeout () {
        if (this._connectionTimeoutId) {
            clearTimeout(this._connectionTimeoutId);
            this._connectionTimeoutId = undefined;
        }
    }

    protected _clearReconnectTimeout () {
        if (this._reconnectTimeoutId) {
            clearTimeout(this._reconnectTimeoutId);
            this._reconnectTimeoutId = undefined;
        }
    }

    protected _scheduleReconnect () {
        if (this._disconnecting) {
            return;
        }

        const delay = Math.min(
            1000 * 2 ** this._reconnectAttempts,
            this._options.maxReconnectDelay,
        );

        this._reconnectAttempts++;

        this._clearReconnectTimeout();
        this._reconnectTimeoutId = setTimeout(() => {
            this._reconnectTimeoutId = undefined;
            if (this._disconnecting) {
                return;
            }
            this.connect();
        }, delay);
        this._unrefTimer(this._reconnectTimeoutId);
    }

    protected _unrefTimer (timer: ReturnType<typeof setTimeout> | ReturnType<typeof setInterval>) {
        (timer as typeof timer & { unref?: () => void }).unref?.();
    }

    public getState (): ConnectionState {
        return this._connectionState.value as ConnectionState;
    }

    public abstract disconnect (): void;

    public createChannel<Definition extends AnyPondChannelDefinition> (definition: Definition, ...args: DefinedChannelArguments<Definition>): Channel<SchemaOf<Definition>>;

    public createChannel<Schema extends AnyPondSchema = AnyPondSchema> (name: string, params?: JoinParamsOf<Schema>): Channel<Schema>;

    public createChannel<Schema extends AnyPondSchema = AnyPondSchema> (
        nameOrDefinition: string | AnyPondChannelDefinition,
        paramsOrOptions: JoinParamsOf<Schema> | DefinedChannelOptions<AnyPondChannelDefinition> = {},
    ) {
        const definition = isPondChannelDefinition(nameOrDefinition) ? nameOrDefinition : null;
        const definitionOptions = definition ? paramsOrOptions as DefinedChannelOptions<AnyPondChannelDefinition> : null;
        const name: string = definition ? buildPondRoute(definition.path, definitionOptions?.params ?? {}) : nameOrDefinition as string;
        const params = definition ? definitionOptions?.joinParams ?? {} : paramsOrOptions as JoinParamsOf<Schema>;
        const joinTimeout = definitionOptions?.joinTimeout ?? this._options.joinTimeout;
        const channel = this.#channels.get(name);

        if (channel && channel.channelState !== ChannelState.CLOSED) {
            return channel;
        }

        const publisher = this._createPublisher();
        const newChannel = new Channel<Schema>(
            publisher,
            this._connectionState,
            name,
            params as JoinParamsOf<Schema>,
            100,
            joinTimeout,
        );

        this.#channels.set(name, newChannel);

        return newChannel;
    }

    public onConnectionChange (callback: (state: ConnectionState) => void): Unsubscribe {
        return this._connectionState.subscribe(callback);
    }

    public onError (callback: (error: Error) => void): Unsubscribe {
        return this._errorSubject.subscribe(callback);
    }

    protected abstract _createPublisher (): (message: ClientMessage) => void;

    protected _parseMessages (data: string): ChannelEvent[] {
        const events: ChannelEvent[] = [];
        const lines = data.trim().split('\n');

        for (const line of lines) {
            if (line.trim()) {
                const parsed = JSON.parse(line);
                const event = channelEventSchema.parse(parsed);

                events.push(event);
            }
        }

        return events;
    }

    protected _clearChannels () {
        this.#channels.forEach((channel) => channel.leave());
        this.#channels.clear();
    }

    #handleAcknowledge (message: ChannelEvent) {
        const channel = this.#channels.get(message.channelName);

        if (!channel) {
            return;
        }

        channel.acknowledge(this._broadcaster, message.requestId);
    }

    #handleError (message: ChannelEvent) {
        const channel = this.#channels.get(message.channelName);

        if (channel) {
            const payload = message.payload as Record<string, any>;
            const nestedError = typeof payload.error === 'object' && payload.error ? payload.error as Record<string, any> : {};
            const numericCode = typeof payload.code === 'number' ? payload.code : undefined;
            const normalized: PondErrorPayload = {
                code: typeof payload.code === 'string' ? payload.code : message.event,
                message: payload.message ?? nestedError.message ?? 'Channel request failed',
                status: payload.status ?? payload.statusCode ?? numericCode ?? nestedError.status ?? 500,
                details: payload.details,
            };

            channel.decline(normalized, message.requestId);
        }
    }

    #init () {
        this._broadcaster.subscribe((message) => {
            if (message.event === Events.ACKNOWLEDGE) {
                this.#handleAcknowledge(message);
            } else if (message.action === ServerActions.ERROR || message.event === Events.UNAUTHORIZED) {
                this.#handleError(message);
            } else if (message.event === Events.CONNECTION && message.action === ServerActions.CONNECT) {
                this._connectionState.publish(ConnectionState.CONNECTED);
            }
        });
    }
}
