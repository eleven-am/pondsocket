import {
    AnyPondChannelDefinition,
    getPondMetadata,
    PondChannelClassMetadata,
    PondEndpointClassMetadata,
    PondEndpointDefinition,
    PondHandlerDefinition,
    PondHandlerLifecycle,
    pondChannelMetadataKey,
    pondEndpointMetadataKey,
    pondHandlerMetadataKey,
    PondSocket,
} from '@eleven-am/pondsocket';
import type { Endpoint, IDistributedBackend } from '@eleven-am/pondsocket/types';
import { Logger, OnModuleDestroy, OnModuleInit, PipeTransform, Type } from '@nestjs/common';
import { DiscoveryService, HttpAdapterHost, ModuleRef } from '@nestjs/core';

import { manageChannel } from '../managers/channel';
import { manageChannelInstance } from '../managers/channelInstance';
import { manageConnection } from '../managers/connection';
import { manageEndpoint } from '../managers/endpoint';
import { manageEndpointInstance } from '../managers/endpointInstance';
import { manageEvent } from '../managers/event';
import { manageJoin } from '../managers/join';
import { manageLeave } from '../managers/leave';
import { manageOutgoingEvent } from '../managers/outgoingEvent';
import { performAction } from '../performers/action';
import { logHandlerError, performErrors } from '../performers/errors';
import {
    CanActivate,
    Constructor,
    GroupedInstances,
    HandlerData,
    PondProvider,
} from '../types';

type EndpointRegistration = PondEndpointDefinition | string;
type ChannelRegistration = AnyPondChannelDefinition | string;

export class PondSocketService implements OnModuleInit, OnModuleDestroy {
    private readonly logger = new Logger(PondSocketService.name);

    private socket: PondSocket | null = null;

    constructor (
        private readonly moduleRef: ModuleRef,
        private readonly discovery: DiscoveryService,
        private readonly adapterHost: HttpAdapterHost,
        private readonly globalGuards: Constructor<CanActivate>[],
        private readonly globalPipes: Constructor<PipeTransform>[],
        private readonly isExclusiveSocketServer: boolean,
        private readonly distributedBackend?: IDistributedBackend,
        private readonly maxMessageSize?: number,
        private readonly heartbeatInterval?: number,
    ) {}

    get pondSocket (): PondSocket | null {
        return this.socket;
    }

    isHealthy (): boolean {
        return this.socket?.isHealthy() ?? false;
    }

    async onModuleInit () {
        const instances = this.getGroupedInstances();

        this.socket = new PondSocket({
            server: this.adapterHost.httpAdapter.getHttpServer(),
            closeHttpServerOnShutdown: false,
            exclusiveServer: this.isExclusiveSocketServer,
            distributedBackend: this.distributedBackend,
            maxMessageSize: this.maxMessageSize,
            heartbeatInterval: this.heartbeatInterval,
        });

        await this.socket.ready();
        instances.forEach((instance) => this.manageEndpoint(this.socket!, instance));
    }

    async onModuleDestroy () {
        const socket = this.socket;

        this.socket = null;
        await socket?.closeAsync();
    }

    private manageEndpoint (socket: PondSocket, groupedInstance: GroupedInstances) {
        const instance = groupedInstance.endpoint.instance;
        const registration = this.getEndpointRegistration(groupedInstance.endpoint);
        const { set: setEndpoint } = manageEndpointInstance(instance);

        if (!registration) {
            return;
        }

        const handlers = [
            ...manageConnection(instance).get(),
            ...this.getDefinitionHandlers(instance, registration, 'connection'),
        ];
        const handler = this.getSingleHandler(handlers, 'connection', groupedInstance.endpoint.name);
        const endpoint = socket.createEndpoint(registration as PondEndpointDefinition, async (context) => {
            if (handler) {
                await handler.value(instance, this.moduleRef, this.globalGuards, this.globalPipes, context);
            } else {
                context.accept();
            }
        });
        const endpointPath = typeof registration === 'string' ? registration : registration.path;

        this.logger.log(`${groupedInstance.endpoint.name} {${endpointPath}}`);

        if (handler) {
            this.logger.log(`Mapped {${endpointPath}, CONNECTION}`);
        }

        setEndpoint(endpoint);
        groupedInstance.channels.forEach((channel) => this.manageChannel(channel, endpoint, endpointPath));
    }

    private manageChannel (provider: PondProvider, endpoint: Endpoint, endpointPath: string) {
        const instance = provider.instance;
        const registration = this.getChannelRegistration(provider);

        if (!registration) {
            return;
        }

        const { set: setChannel } = manageChannelInstance(instance);
        const joinHandlers = [
            ...manageJoin(instance).get(),
            ...this.getDefinitionHandlers(instance, registration, 'join'),
        ];
        const joinHandler = this.getSingleHandler(joinHandlers, 'join', provider.name);
        const channelInstance = endpoint.createChannel(registration as AnyPondChannelDefinition, async (context) => {
            if (joinHandler) {
                await joinHandler.value(instance, this.moduleRef, this.globalGuards, this.globalPipes, context);
            } else {
                context.accept();
            }
        });
        const path = typeof registration === 'string' ? registration : registration.path;
        const fullPath = `${endpointPath}/${path}`.replace(/\/+/g, '/');

        this.logger.log(`${provider.name} {${fullPath}}`);

        if (joinHandler) {
            this.logger.log(`Mapped {${fullPath}, JOIN}`);
        }

        setChannel(channelInstance);

        const eventHandlers = [
            ...manageEvent(instance).get(),
            ...this.getDefinitionHandlers(instance, registration, 'event'),
        ];

        this.assertUniquePaths(eventHandlers, 'event', provider.name);
        eventHandlers.forEach((handler) => {
            channelInstance.onEvent(handler.path, async (context) => {
                await handler.value(instance, this.moduleRef, this.globalGuards, this.globalPipes, context);
            });
            this.logger.log(`Mapped {${fullPath}/${handler.path}, EVENT}`);
        });

        const outgoingHandlers = [
            ...manageOutgoingEvent(instance).get(),
            ...this.getDefinitionHandlers(instance, registration, 'outgoing'),
        ];

        this.assertUniquePaths(outgoingHandlers, 'outgoing event', provider.name);
        outgoingHandlers.forEach((handler) => {
            channelInstance.handleOutgoingEvent(handler.path, async (context) => {
                await handler.value(
                    instance,
                    this.moduleRef,
                    this.globalGuards,
                    this.globalPipes,
                    context as any,
                );
            });
            this.logger.log(`Mapped {${fullPath}/${handler.path}, OUTGOING}`);
        });

        const leaveHandlers = [
            ...manageLeave(instance).get(),
            ...this.getDefinitionHandlers(instance, registration, 'leave'),
        ];
        const leaveHandler = this.getSingleHandler(leaveHandlers, 'leave', provider.name);

        if (leaveHandler) {
            channelInstance.onLeave((event) => leaveHandler.value(
                instance,
                this.moduleRef,
                this.globalGuards,
                this.globalPipes,
                event,
            ));
            this.logger.log(`Mapped {${fullPath}, LEAVE}`);
        }
    }

    private getDefinitionHandlers<Context> (
        instance: any,
        registration: EndpointRegistration | ChannelRegistration,
        lifecycle: PondHandlerLifecycle,
    ): HandlerData<Context>[] {
        if (typeof registration === 'string') {
            return [];
        }

        const definitions = getPondMetadata<PondHandlerDefinition[]>(instance, pondHandlerMetadataKey) ?? [];

        return definitions
            .filter((definition) => definition.definition === registration && definition.lifecycle === lifecycle)
            .map((definition) => ({
                path: definition.event ?? '',
                value: async (target, moduleRef, guards, pipes, input) => {
                    const typedTarget = target as any;
                    const method = typedTarget[definition.propertyKey];

                    if (typeof method !== 'function') {
                        throw new Error(`${typedTarget.constructor.name}.${String(definition.propertyKey)} is not a method`);
                    }

                    try {
                        return await performAction(
                            typedTarget,
                            moduleRef,
                            guards,
                            pipes,
                            method,
                            String(definition.propertyKey),
                            input as any,
                            lifecycle,
                            definition.definition,
                        );
                    } catch (error) {
                        if (lifecycle === 'connection' || lifecycle === 'join' || lifecycle === 'event') {
                            return performErrors(error, input as any, lifecycle);
                        }

                        logHandlerError(error, lifecycle, definition.propertyKey);

                        if (lifecycle === 'outgoing') {
                            (input as any).block();
                        }
                    }
                },
            }));
    }

    private getSingleHandler<Context> (handlers: HandlerData<Context>[], lifecycle: string, providerName: string) {
        if (handlers.length > 1) {
            throw new Error(`${providerName} declares multiple ${lifecycle} handlers; exactly one is allowed`);
        }

        return handlers[0];
    }

    private assertUniquePaths<Context> (handlers: HandlerData<Context>[], lifecycle: string, providerName: string) {
        const paths = new Set<string>();

        handlers.forEach(({ path }) => {
            if (paths.has(path)) {
                throw new Error(`${providerName} declares duplicate ${lifecycle} handler "${path}"`);
            }

            paths.add(path);
        });
    }

    private getEndpointRegistration (provider: PondProvider): EndpointRegistration | null {
        const metadata = getPondMetadata<PondEndpointClassMetadata>(provider.metatype, pondEndpointMetadataKey);

        return metadata?.definition ?? manageEndpoint(provider.metatype).get();
    }

    private getChannelRegistration (provider: PondProvider): ChannelRegistration | null {
        const metadata = getPondMetadata<PondChannelClassMetadata>(provider.metatype, pondChannelMetadataKey);

        return metadata?.definition ?? manageChannel(provider.metatype).get();
    }

    private getProviders (): PondProvider[] {
        const wrappers = [...this.discovery.getProviders(), ...this.discovery.getControllers()];
        const providers = new Map<Type, PondProvider>();

        wrappers.forEach((wrapper) => {
            if (wrapper.instance && typeof wrapper.metatype === 'function') {
                providers.set(wrapper.metatype as Type, {
                    host: wrapper.host,
                    instance: wrapper.instance,
                    metatype: wrapper.metatype as Type,
                    name: String(wrapper.name ?? wrapper.metatype.name),
                });
            }
        });

        return [...providers.values()];
    }

    private getGroupedInstances (): GroupedInstances[] {
        const providers = this.getProviders();
        const endpoints = providers.filter((provider) => this.getEndpointRegistration(provider));
        const channels = providers.filter((provider) => this.getChannelRegistration(provider));

        if (channels.length > 0 && endpoints.length === 0) {
            throw new Error('PondSocket channels were discovered, but no endpoint is registered');
        }

        const endpointByType = new Map(endpoints.map((endpoint) => [endpoint.metatype, endpoint]));
        const grouped = new Map(endpoints.map((endpoint) => [endpoint, [] as PondProvider[]]));
        const endpointPaths = new Set<string>();

        endpoints.forEach((endpoint) => {
            const registration = this.getEndpointRegistration(endpoint)!;
            const path = typeof registration === 'string' ? registration : registration.path;

            if (endpointPaths.has(path)) {
                throw new Error(`Multiple PondSocket endpoints declare the path "${path}"`);
            }

            endpointPaths.add(path);
        });

        channels.forEach((channel) => {
            const metadata = getPondMetadata<PondChannelClassMetadata>(channel.metatype, pondChannelMetadataKey);
            const reference = metadata?.options.endpoint;
            let endpoint: PondProvider | undefined;

            if (reference) {
                let endpointType = reference as Type;

                if (!endpointByType.has(endpointType)) {
                    try {
                        endpointType = (reference as () => Type)();
                    } catch {
                        throw new Error(`${channel.name} references an invalid PondSocket endpoint`);
                    }
                }

                endpoint = endpointByType.get(endpointType);

                if (!endpoint) {
                    throw new Error(`${channel.name} references endpoint ${endpointType.name}, but it is not registered as a Nest provider`);
                }
            } else if (endpoints.length === 1) {
                endpoint = endpoints[0];
            } else {
                const localEndpoints = endpoints.filter((candidate) => candidate.host === channel.host);

                if (localEndpoints.length === 1) {
                    endpoint = localEndpoints[0];
                }
            }

            if (!endpoint) {
                throw new Error(`${channel.name} cannot be associated with an endpoint. Pass { endpoint: EndpointClass } to Channel().`);
            }

            grouped.get(endpoint)!.push(channel);
        });

        return [...grouped].map(([endpoint, groupedChannels]) => ({
            endpoint,
            channels: groupedChannels,
        }));
    }
}
