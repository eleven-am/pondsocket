import type {
	ConnectionContext,
	EventContext,
	IDistributedBackend,
	JoinContext,
	LeaveEvent,
	PondAssigns,
	PondMessage,
	PondPresence,
} from '@eleven-am/pondsocket/types';
import type {DiscoveredClass} from '@golevelup/nestjs-discovery/lib/discovery.interfaces';
import type {ModuleMetadata, PipeTransform} from '@nestjs/common';
import type {ModuleRef} from '@nestjs/core';

import type {Context} from './context/context';

export interface NestContext {
    connection?: ConnectionContext<string>;
    join?: JoinContext<string>;
    event?: EventContext<string>;
    leave?: LeaveEvent;
}

export type ParamDecoratorCallback<Input> = (data: Input, context: Context, type: unknown) => unknown | Promise<unknown>;

export interface ParamDecoratorMetadata {
    index: number;
    callback: (context: Context, globalPipes: Constructor<PipeTransform>[], moduleRef: ModuleRef) => Promise<unknown>;
}

export type HandlerFunction<Context> = (instance: unknown, moduleRef: ModuleRef, globalGuards: Constructor<CanActivate>[], globalPipes: Constructor<PipeTransform>[], ctx: Context) => Promise<void>;

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
    endpoint: DiscoveredClass;
    channels: DiscoveredClass[];
}

export interface Metadata extends Omit<ModuleMetadata, 'controllers'> {
    guards?: Constructor<CanActivate>[];
    pipes?: Constructor<PipeTransform>[];
    isExclusiveSocketServer?: boolean;
	backend?: IDistributedBackend;
    isGlobal?: boolean;
}

export interface AsyncMetadata extends Omit<Metadata, 'backend'> {
	isGlobal?: boolean;
	inject?: any[];
	imports?: any[];
	useFactory: (...args: any[]) => Promise<IDistributedBackend> | IDistributedBackend;
}

export type PondResponse<Event extends string = string, Payload extends PondMessage = PondMessage, Presence extends PondPresence = PondPresence, Assigns extends PondAssigns = PondAssigns> = {
    event?: Event;
    broadcast?: Event;
    broadcastFrom?: Event;
    assigns?: Partial<Assigns>;
    presence?: Partial<Presence>;
    broadcastTo?: {
        event: Event;
        users: string[];
    };
} & Payload;
