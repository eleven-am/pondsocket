import type {
	Channel as PondChannel,
	ConnectionContext,
	EventContext,
	IDistributedBackend,
	JoinContext,
	JoinParams,
	LeaveEvent,
	Params,
	PondAssigns,
	PondEvent,
	PondEventMap,
	PondMessage,
	PondPresence,
	UserData,
} from '@eleven-am/pondsocket/types';
import type {DynamicModule, ModuleMetadata, PipeTransform} from '@nestjs/common';

type Constructor<T> = new (...args: any[]) => T;

type ParamDecoratorCallback<Input, ParamType> = (data: Input, context: Context, type: ParamType) => unknown | Promise<unknown>;

interface CanActivate {

    /**
     * @desc Whether the client can continue with the request
     * @param context - The context of the request
     */
    canActivate(context: Context): boolean | Promise<boolean>;
}

interface Metadata extends Omit<ModuleMetadata, 'controllers'> {
    guards?: Constructor<CanActivate>[];
    pipes?: Constructor<PipeTransform>[];
    isExclusiveSocketServer?: boolean;
    isGlobal?: boolean;
}

interface AsyncMetadata extends Omit<Metadata, 'backend'> {
	isGlobal?: boolean;
	inject?: any[];
	imports?: any[];
	useFactory: (...args: any[]) => Promise<IDistributedBackend> | IDistributedBackend;
}

type NestFuncType<Event extends string, Payload extends PondMessage, Presence extends PondPresence, Assigns extends PondAssigns = PondAssigns> = {
    event?: Event;
    broadcast?: Event;
    broadcastFrom?: Event;
    assigns?: Partial<Assigns>;
    presence?: Presence;
} & Payload;

type PondResponse<EventType extends PondEventMap = PondEventMap, Event extends keyof EventType = string, Presence extends PondPresence = PondPresence, Assigns extends PondAssigns = PondAssigns> =
    Event extends string ?
        EventType[Event] extends [PondMessage, PondMessage] ? NestFuncType<Event, EventType[Event][1], Presence, Assigns> :
            EventType[Event] extends PondMessage ? NestFuncType<Event, EventType[Event], Presence, Assigns> :
                never :
        never;

declare class Context<Path extends string = string, Event extends PondEventMap = PondEventMap, Presence extends PondPresence = PondPresence, Assigns extends PondAssigns = PondAssigns> {
    /**
     * @desc The request object, available in onJoin handlers
     */
    get joinContext (): JoinContext<Path, Event, Presence, Assigns> | null;

    /**
     * @desc The request object, available in onEvent handlers
     */
    get eventContext (): EventContext<Path, Event, Presence, Assigns> | null;

    /**
     * @desc The request object, available in onConnection handlers
     */
    get connectionContext (): ConnectionContext<Path> | null;

    /**
     * @desc The request object, available in onLeave handlers
     */
    get leaveEvent (): LeaveEvent | null;

    /**
     * @desc The presence object, available on the channel
     */
    get presence (): Presence;

    /**
     * @desc The assigns object, available on the channel
     */
    get assigns (): Assigns;

    /**
     * @desc The user object, assigned to the current user
     */
    get user (): UserData<Presence, Assigns> | null;

    /**
     * @desc The channel object handling the request
     */
    get channel (): PondChannel<Event, Presence, Assigns> | null;

    /**
     * @desc The event object of the current the request
     */
    get event (): PondEvent<Path> | null;

    /**
     * @desc The class reference of the current context
     */
    getClass (): any;

    /**
     * @desc The method reference of the current context
     */
    getHandler (): any;

    /**
     * @desc The instance reference of the current context
     */
    getInstance(): any;

    /**
     * @desc The path of the current context
     */
    getMethod(): string;

    /**
     * @desc Saves data to the current context
     */
    addData <Type> (key: string, value: Type): void;

    /**
     * @desc Retrieves data from the current context
     */
    getData <Type> (key: string): Type | null;
}

/**
 * @desc Decorator that marks a class as a channel
 */
declare function Channel(path?: string): ClassDecorator

/**
 * @desc Decorator that marks a property on a class as the channel instance
 * @returns {Channel | PondChannel}
 */
declare function ChannelInstance(channel?: string): PropertyDecorator;

/**
 * @desc Decorator that marks a class as an endpoint
 */
declare function Endpoint(path?: string): ClassDecorator;

/**
 * @desc Decorator that marks a property on a class as the endpoint instance
 * @returns {Endpoint}
 */
declare function EndpointInstance(): PropertyDecorator;

/**
 * @desc Parameter decorator that retrieves the current channel
 * @returns {Channel}
 */
declare const GetChannel: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current connection headers
 * @returns {IncomingHttpHeaders}
 */
declare const GetConnectionHeaders: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current connection context
 * @returns {ConnectionContext<string>}
 */
declare const GetConnectionContext: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current context
 * @returns {Context}
 */
declare const GetContext: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current event params
 * @returns {Params}
 */
declare const GetEventParams: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current event payload
 * @returns {PondMessage}
 */
declare const GetEventPayload: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current event query
 * @returns {Record<string, string>}
 */
declare const GetEventQuery: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current event context
 * @returns {EventContext<string>}
 */
declare const GetEventContext: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current join params
 * @returns {JoinParams}
 */
declare const GetJoinParams: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current join context
 * @returns {JoinContext<string>}
 */
declare const GetJoinContext: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current leave event
 * @returns {LeaveEvent}
 */
declare const GetLeaveEvent: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current user
 * @returns {UserData}
 */
declare const GetUserData: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current user presences
 * @returns {PondAssigns}
 */
declare const GetUserAssigns: () => ParameterDecorator;

/**
 * @desc Parameter decorator that retrieves the current user presences
 * @returns {PondPresence}
 */
declare const GetUserPresence: () => ParameterDecorator;

/**
 * @desc Method decorator that marks a method as an onConnection handler
 */
declare function OnConnectionRequest(): MethodDecorator;

/**
 * @desc Method decorator that marks a method as an onEvent handler
 */
declare function OnEvent<EventType extends PondEvenType = PondEvenType>(path?: keyof EventType): MethodDecorator;

/**
 * @desc Method decorator that marks a method as an onJoin handler
 */
declare function OnJoinRequest(): MethodDecorator;

/**
 * @desc Method decorator that marks a method as an onLeave handler
 */
declare function OnLeave(): MethodDecorator;

/**
 * @desc Decorator that adds guards to a channel's or endpoint's class or method
 * @param guards - The guards to add. It is important to add the guards to a providers array in any module (only necessary if the guards inject dependencies)
 */
declare function UseGuards (...guards: Constructor<CanActivate>[]): ClassDecorator | MethodDecorator;

/**
 * @desc Decorator that adds validators to a channel's or endpoint's class or method
 * @param validators - The validators to add. It is important to add the validators to a providers array in any module (only necessary if the validators inject dependencies)
 */
declare function UsePipes (...validators: Constructor<PipeTransform>[]): ClassDecorator | MethodDecorator;

/**
 * @desc Helper function that creates a parameter decorator
 * @param callback - The callback to run when the parameter is being retrieved
 */
declare function createParamDecorator<Input, ParamType> (callback: ParamDecoratorCallback<Input, ParamType>): (data: Input) => ParameterDecorator;

declare class PondSocketModule {
    static forRoot({ guards, providers, imports, exports, isGlobal }: Metadata): DynamicModule;
	
	static forRootAsync({ guards, providers, imports, exports, isGlobal, inject, useFactory }: AsyncMetadata): DynamicModule;
}

