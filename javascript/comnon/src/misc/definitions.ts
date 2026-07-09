import { buildPondRoute, compilePondRoute } from './route';
import {
    AnyPondSchema,
    EventsOf,
    Params,
    PondSchema,
} from './types';

export const pondEndpointMetadataKey = Symbol.for('pondsocket.endpoint.definition');
export const pondChannelMetadataKey = Symbol.for('pondsocket.channel.definition');
export const pondHandlerMetadataKey = Symbol.for('pondsocket.handler.definitions');
export const pondParameterMetadataKey = Symbol.for('pondsocket.parameter.definitions');

const pondSchemaToken = Symbol('pondsocket.schema.token');

export type Constructor<T = object> = abstract new (...args: any[]) => T;
export type LazyConstructor<T = object> = () => Constructor<T>;

export interface PondSchemaToken<Schema extends AnyPondSchema> {
    readonly kind: 'pondsocket.schema';
    readonly [pondSchemaToken]: Schema;
}

export interface PondEndpointDefinition<Path extends string = string> {
    readonly kind: 'pondsocket.endpoint';
    readonly path: Path;
    Endpoint(): ClassDecorator;
    GetContext(): ParameterDecorator;
    OnConnection(): MethodDecorator;
}

export interface PondChannelDecoratorOptions {
    endpoint?: Constructor | LazyConstructor;
}

export type PondHandlerLifecycle = 'connection' | 'join' | 'event' | 'leave' | 'outgoing';

export interface PondHandlerDefinition {
    definition: PondChannelDefinition | PondEndpointDefinition;
    event?: string;
    lifecycle: PondHandlerLifecycle;
    propertyKey: string | symbol;
}

export interface PondParameterDefinition {
    definition: PondChannelDefinition | PondEndpointDefinition;
    index: number;
    propertyKey: string | symbol;
    source: 'context';
}

export interface PondEndpointClassMetadata {
    definition: PondEndpointDefinition;
}

export interface PondChannelClassMetadata {
    definition: PondChannelDefinition;
    options: PondChannelDecoratorOptions;
}

export interface PondChannelDefinition<
    Schema extends AnyPondSchema = AnyPondSchema,
    Path extends string = string,
> {
    readonly kind: 'pondsocket.channel';
    readonly path: Path;
    readonly schema: PondSchemaToken<Schema>;
    build(params: Params<Path>): string;
    Channel(options?: PondChannelDecoratorOptions): ClassDecorator;
    GetContext(): ParameterDecorator;
    OnJoin(): MethodDecorator;
    OnLeave(): MethodDecorator;
    OnEvent<Event extends Extract<keyof EventsOf<Schema>, string>>(event: Event): MethodDecorator;
    OnOutgoingEvent<Event extends Extract<keyof EventsOf<Schema>, string>>(event: Event): MethodDecorator;
}

export type AnyPondChannelDefinition = PondChannelDefinition<AnyPondSchema, string>;
export type SchemaOf<Definition extends AnyPondChannelDefinition> = Definition extends PondChannelDefinition<infer Schema, string> ? Schema : never;
export type PathOf<Definition extends AnyPondChannelDefinition> = Definition extends PondChannelDefinition<AnyPondSchema, infer Path> ? Path : never;

function defineMetadata (target: object, key: symbol, value: unknown) {
    Object.defineProperty(target, key, {
        configurable: true,
        value,
    });

    const reflector = Reflect as typeof Reflect & {
        defineMetadata?: (metadataKey: symbol, metadataValue: unknown, metadataTarget: object) => void;
    };

    reflector.defineMetadata?.(key, value, target);
}

function addHandlerDefinition (target: object, definition: PondHandlerDefinition) {
    const inherited = (target as Record<symbol, PondHandlerDefinition[]>)[pondHandlerMetadataKey] ?? [];

    defineMetadata(target, pondHandlerMetadataKey, [...inherited, definition]);
}

function addParameterDefinition (target: object, definition: PondParameterDefinition) {
    const inherited = (target as Record<symbol, PondParameterDefinition[]>)[pondParameterMetadataKey] ?? [];

    defineMetadata(target, pondParameterMetadataKey, [...inherited, definition]);
}

function getContextDecorator (
    definition: PondChannelDefinition | PondEndpointDefinition,
): ParameterDecorator {
    return (target, propertyKey, index) => {
        if (propertyKey === undefined) {
            throw new Error('PondSocket context decorators can only be used on methods');
        }

        addParameterDefinition(target, {
            definition,
            index,
            propertyKey,
            source: 'context',
        });
    };
}

export function getPondMetadata<T> (target: object, key: symbol): T | undefined {
    const reflector = Reflect as typeof Reflect & {
        getMetadata?: (metadataKey: symbol, metadataTarget: object) => unknown;
    };

    return (reflector.getMetadata?.(key, target) ?? (target as Record<symbol, unknown>)[key]) as T | undefined;
}

export function definePondSchema<Schema extends PondSchema> (): PondSchemaToken<Schema> {
    return Object.freeze({
        kind: 'pondsocket.schema' as const,
    }) as PondSchemaToken<Schema>;
}

export function isPondEndpointDefinition (value: unknown): value is PondEndpointDefinition {
    return typeof value === 'object' && value !== null && (value as PondEndpointDefinition).kind === 'pondsocket.endpoint';
}

export function isPondChannelDefinition (value: unknown): value is AnyPondChannelDefinition {
    return typeof value === 'object' && value !== null && (value as AnyPondChannelDefinition).kind === 'pondsocket.channel';
}

export function definePondEndpoint<const Path extends string> (path: Path): PondEndpointDefinition<Path> {
    compilePondRoute(path);

    const definition: PondEndpointDefinition<Path> = {
        kind: 'pondsocket.endpoint',
        path,
        Endpoint: () => (target) => {
            defineMetadata(target, pondEndpointMetadataKey, {
                definition,
            } satisfies PondEndpointClassMetadata);
        },
        GetContext: () => getContextDecorator(definition),
        OnConnection: () => (target, propertyKey) => {
            addHandlerDefinition(target, {
                definition,
                lifecycle: 'connection',
                propertyKey,
            });
        },
    };

    return Object.freeze(definition);
}

export function definePondChannel<
    Schema extends AnyPondSchema,
    const Path extends string = '*',
> (schema: PondSchemaToken<Schema>, path?: Path): PondChannelDefinition<Schema, Path> {
    const resolvedPath = (path ?? '*') as Path;

    compilePondRoute(resolvedPath);

    const definition: PondChannelDefinition<Schema, Path> = {
        kind: 'pondsocket.channel',
        path: resolvedPath,
        schema,
        build: (params) => buildPondRoute(resolvedPath, params),
        Channel: (options = {}) => (target) => {
            defineMetadata(target, pondChannelMetadataKey, {
                definition,
                options,
            } satisfies PondChannelClassMetadata);
        },
        GetContext: () => getContextDecorator(definition),
        OnJoin: () => (target, propertyKey) => {
            addHandlerDefinition(target, {
                definition,
                lifecycle: 'join',
                propertyKey,
            });
        },
        OnLeave: () => (target, propertyKey) => {
            addHandlerDefinition(target, {
                definition,
                lifecycle: 'leave',
                propertyKey,
            });
        },
        OnEvent: (event) => (target, propertyKey) => {
            compilePondRoute(event);
            addHandlerDefinition(target, {
                definition,
                event,
                lifecycle: 'event',
                propertyKey,
            });
        },
        OnOutgoingEvent: (event) => (target, propertyKey) => {
            compilePondRoute(event);
            addHandlerDefinition(target, {
                definition,
                event,
                lifecycle: 'outgoing',
                propertyKey,
            });
        },
    };

    return Object.freeze(definition);
}
