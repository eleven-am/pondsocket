import type {
    AnyPondChannelDefinition,
    AnyPondSchema,
    AssignsOf,
    ConnectionContext,
    EventContext as CoreEventContext,
    EventPayload,
    EventReplyPayload,
    EventsOf,
    IDistributedBackend,
    JoinContext as CoreJoinContext,
    LeaveEvent,
    OutgoingContext as CoreOutgoingContext,
    OutgoingPayload,
    PathOf,
    Params,
    PondAssigns,
    PondHandlerLifecycle,
    PondEndpointDefinition,
    PondMessage,
    PondPresence,
    PresenceOf,
    SchemaOf,
    ServerActions,
} from '@eleven-am/pondsocket/types';
import type { ModuleMetadata, PipeTransform, Type } from '@nestjs/common';
import type { ModuleRef } from '@nestjs/core';

import type { Context } from './context/context';

export interface NestContext<
    Schema extends AnyPondSchema = AnyPondSchema,
    Path extends string = string,
    EventName extends Extract<keyof EventsOf<Schema>, string> = Extract<keyof EventsOf<Schema>, string>,
> {
    connection?: ConnectionContext<Path>;
    join?: CoreJoinContext<Path, Schema>;
    event?: CoreEventContext<Path, Schema, EventName>;
    outgoing?: CoreOutgoingContext<Path, Schema, EventName>;
    leave?: LeaveEvent;
}

export type ParamDecoratorCallback<Input> = (data: Input, context: Context, type: unknown) => unknown | Promise<unknown>;

export interface ParamDecoratorMetadata {
    index: number;
    callback: (context: Context, globalPipes: Constructor<PipeTransform>[], moduleRef: ModuleRef) => Promise<unknown>;
}

export type HandlerFunction<Context> = (instance: unknown, moduleRef: ModuleRef, globalGuards: Constructor<CanActivate>[], globalPipes: Constructor<PipeTransform>[], ctx: Context) => Promise<unknown>;

export type HandlerData<Context> = {
    path: string;
    value: HandlerFunction<Context>;
}

export type Constructor<T, Parameters extends any[] = any[]> = new (...args: Parameters) => T;

export interface CanActivate {

    /**
     * @desc Whether the client can continue with the request
     * @param context - The context of the request
     */
    canActivate(context: Context): boolean | Promise<boolean>;
}

export type GroupedInstances = {
    endpoint: PondProvider;
    channels: PondProvider[];
}

export interface PondProvider {
    host?: {
        metatype?: Type;
    };
    instance: any;
    metatype: Type;
    name: string;
}

export interface Metadata extends Omit<ModuleMetadata, 'controllers'> {
    guards?: Constructor<CanActivate>[];
    pipes?: Constructor<PipeTransform>[];
    isExclusiveSocketServer?: boolean;
    backend?: IDistributedBackend;
    maxMessageSize?: number;
    heartbeatInterval?: number;
    isGlobal?: boolean;
}

export interface AsyncFactoryResult {
    backend?: IDistributedBackend;
    maxMessageSize?: number;
    heartbeatInterval?: number;
}

export interface AsyncMetadata extends Omit<Metadata, 'backend' | 'maxMessageSize' | 'heartbeatInterval'> {
    isGlobal?: boolean;
    inject?: any[];
    imports?: any[];
    useFactory: (...args: any[]) => Promise<AsyncFactoryResult> | AsyncFactoryResult;
}

export type PondResponse<Event extends string = string, Payload extends PondMessage = PondMessage, Presence extends PondPresence = PondPresence, Assigns extends PondAssigns = PondAssigns> = {
    event?: Event;
    eventParams?: Params<Event>;
    broadcast?: Event;
    broadcastFrom?: Event;
    assigns?: Partial<Assigns>;
    presence?: Partial<Presence>;
    broadcastTo?: {
        event: Event;
        users: string[];
    };
    decline?: string | {
        message?: string;
        status?: number;
    };
    payload?: Payload;
} & Partial<Payload>;

export type PondLifecycle = PondHandlerLifecycle;

type EndpointPathOf<Definition extends PondEndpointDefinition> =
    Definition extends PondEndpointDefinition<infer Path> ? Path : never;

export type PondConnectionContext<Definition extends PondEndpointDefinition> =
    Omit<Context<AnyPondSchema, EndpointPathOf<Definition>>,
        'block' |
        'broadcast' |
        'broadcastFrom' |
        'broadcastTo' |
        'channel' |
        'eventContext' |
        'evictUser' |
        'joinContext' |
        'joinParams' |
        'leaveEvent' |
        'outgoingContext' |
        'payload' |
        'presence' |
        'removePresence' |
        'trackPresence' |
        'transform' |
        'updatePresence'
    > & {
        readonly connectionContext: ConnectionContext<EndpointPathOf<Definition>>;
        readonly params: Params<EndpointPathOf<Definition>>;
    };

export type PondJoinContext<Definition extends AnyPondChannelDefinition> =
    Omit<Context<SchemaOf<Definition>, PathOf<Definition>>,
        'block' |
        'connectionContext' |
        'eventContext' |
        'evictUser' |
        'leaveEvent' |
        'outgoingContext' |
        'payload' |
        'removePresence' |
        'transform' |
        'updatePresence'
    > & {
        readonly joinContext: CoreJoinContext<PathOf<Definition>, SchemaOf<Definition>>;
        readonly joinParams: SchemaOf<Definition>['joinParams'];
    };

export type PondEventContext<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
> = Omit<Context<SchemaOf<Definition>, Event, Event>,
    'accept' |
    'block' |
    'connectionContext' |
    'decline' |
    'joinContext' |
    'joinParams' |
    'leaveEvent' |
    'outgoingContext' |
    'reply' |
    'transform'
> & {
        readonly eventContext: CoreEventContext<Event, SchemaOf<Definition>, Event>;
        readonly event: CoreEventContext<Event, SchemaOf<Definition>, Event>['event'];
        readonly payload: EventPayload<EventsOf<SchemaOf<Definition>>, Event>;
        reply(payload: EventReplyPayload<EventsOf<SchemaOf<Definition>>, Event>): PondEventContext<Definition, Event>;
    };

type PondOutgoingContextForAction<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
    Action extends ServerActions.BROADCAST | ServerActions.SYSTEM,
> = Omit<Context<SchemaOf<Definition>, Event, Event>,
    'accept' |
    'action' |
    'assign' |
    'broadcast' |
    'broadcastFrom' |
    'broadcastTo' |
    'connectionContext' |
    'decline' |
    'eventContext' |
    'evictUser' |
    'joinContext' |
    'joinParams' |
    'leaveEvent' |
    'removePresence' |
    'reply' |
    'event' |
    'outgoingContext' |
    'payload' |
    'trackPresence' |
    'transform' |
    'updatePresence'
> & {
        readonly action: Action;
        readonly outgoingContext: CoreOutgoingContext<Event, SchemaOf<Definition>, Event, Action>;
        readonly event: CoreOutgoingContext<Event, SchemaOf<Definition>, Event, Action>['event'];
        readonly payload: OutgoingPayload<SchemaOf<Definition>, Event, Action>;
        transform(payload: OutgoingPayload<SchemaOf<Definition>, Event, Action>): PondOutgoingContextForAction<Definition, Event, Action>;
    };

export type PondOutgoingContext<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
> = PondOutgoingContextForAction<Definition, Event, ServerActions.BROADCAST> |
    PondOutgoingContextForAction<Definition, Event, ServerActions.SYSTEM>;

export type PondCoreJoinContext<Definition extends AnyPondChannelDefinition> =
    CoreJoinContext<PathOf<Definition>, SchemaOf<Definition>>;

export type PondCoreEventContext<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
> = CoreEventContext<Event, SchemaOf<Definition>, Event>;

export type PondCoreOutgoingContext<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
> = CoreOutgoingContext<Event, SchemaOf<Definition>, Event, ServerActions.BROADCAST> |
    CoreOutgoingContext<Event, SchemaOf<Definition>, Event, ServerActions.SYSTEM>;

export type PondEventPayload<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
> = EventPayload<EventsOf<SchemaOf<Definition>>, Event>;

export type PondSchemaPresence<Definition extends AnyPondChannelDefinition> = PresenceOf<SchemaOf<Definition>>;
export type PondSchemaAssigns<Definition extends AnyPondChannelDefinition> = AssignsOf<SchemaOf<Definition>>;

export type PondResponseFor<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
> = Omit<PondResponse<
    Event,
    EventReplyPayload<EventsOf<SchemaOf<Definition>>, Event>,
    PresenceOf<SchemaOf<Definition>>,
    AssignsOf<SchemaOf<Definition>>
>, 'eventParams'> & {
    eventParams?: Params<Event>;
};

export type PondEventHandler<
    Definition extends AnyPondChannelDefinition,
    Event extends Extract<keyof EventsOf<SchemaOf<Definition>>, string>,
> = (context: PondEventContext<Definition, Event>) =>
    PondResponseFor<Definition, Event> | null | undefined |
    Promise<PondResponseFor<Definition, Event> | null | undefined>;
