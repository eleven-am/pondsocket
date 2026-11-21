import 'reflect-metadata';
import { createServer } from 'http';

import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
    PondSchema,
} from '@eleven-am/pondsocket';
import { Scope } from '@nestjs/common';

import { ChannelInstance } from '../decorators/channelInstance';
import { EndpointInstance } from '../decorators/endpointInstance';
import { PondSocketService } from '../services/pondSocket';

interface ChatSchema extends PondSchema<{
    message: { text: string };
}, { online: boolean }, { role: string }, { token: string }> {}

const schema = definePondSchema<ChatSchema>();

function provider (metatype: any, host: object) {
    return {
        host,
        instance: new metatype(),
        metatype,
        name: metatype.name,
    };
}

function createService (providers: any[]) {
    const server = createServer();
    const discovery = {
        getControllers: jest.fn().mockReturnValue([]),
        getProviders: jest.fn().mockReturnValue(providers),
    };
    const service = new PondSocketService(
        { get: jest.fn(), resolve: jest.fn() } as any,
        discovery as any,
        { httpAdapter: { getHttpServer: () => server } } as any,
        [],
        [],
        false,
    );

    return { server, service };
}

describe('PondSocketService discovery', () => {
    it('emits constructor injection metadata without Injectable', () => {
        const Chat = definePondChannel(schema, '/dependency-chat');

        class ChatDependency {}

        @Chat.Channel()
        class ChatChannel {
            constructor (readonly dependency: ChatDependency) {}
        }

        expect(Reflect.getMetadata('design:paramtypes', ChatChannel)).toEqual([ChatDependency]);
    });

    it('uses explicit endpoint classes to group shared channel definitions', async () => {
        const SocketA = definePondEndpoint('/socket-a');
        const SocketB = definePondEndpoint('/socket-b');
        const ChatA = definePondChannel(schema, '/chat-a/:roomId');
        const ChatB = definePondChannel(schema, '/chat-b/:roomId');

        @SocketA.Endpoint()
        class EndpointA {}

        @SocketB.Endpoint()
        class EndpointB {}

        @ChatA.Channel({ endpoint: EndpointA })
        class ChannelA {}

        @ChatB.Channel({ endpoint: () => EndpointB })
        class ChannelB {}

        const host = {};
        const { service } = createService([
            provider(EndpointA, host),
            provider(EndpointB, host),
            provider(ChannelA, host),
            provider(ChannelB, host),
        ]);

        await service.onModuleInit();

        expect(service.pondSocket).not.toBeNull();
        expect(service.isHealthy()).toBe(true);

        await service.onModuleDestroy();
        expect(service.pondSocket).toBeNull();
    });

    it('initializes and destroys idempotently and rejects restart after shutdown', async () => {
        const Socket = definePondEndpoint('/lifecycle');

        @Socket.Endpoint()
        class SocketEndpoint {}

        const { service } = createService([provider(SocketEndpoint, {})]);
        const firstInitialization = service.onModuleInit();
        const secondInitialization = service.onModuleInit();

        expect(secondInitialization).toBe(firstInitialization);
        await firstInitialization;

        const firstDestroy = service.onModuleDestroy();
        const secondDestroy = service.onModuleDestroy();

        expect(secondDestroy).toBe(firstDestroy);
        await firstDestroy;
        await expect(service.onModuleInit()).rejects.toThrow(/cannot initialize after shutdown/);
    });

    it('clears injected endpoint and channel references during shutdown', async () => {
        const Socket = definePondEndpoint('/references');
        const Chat = definePondChannel(schema, '/references-chat');

        @Socket.Endpoint()
        class SocketEndpoint {
            @EndpointInstance()
            endpoint!: unknown;
        }

        @Chat.Channel({ endpoint: SocketEndpoint })
        class ChatChannel {
            @ChannelInstance()
            channel!: unknown;
        }

        const endpointProvider = provider(SocketEndpoint, {});
        const channelProvider = provider(ChatChannel, {});
        const { service } = createService([endpointProvider, channelProvider]);

        await service.onModuleInit();
        expect(endpointProvider.instance.endpoint).toBeDefined();
        expect(channelProvider.instance.channel).toBeDefined();

        await service.onModuleDestroy();
        expect(endpointProvider.instance.endpoint).toBeUndefined();
        expect(channelProvider.instance.channel).toBeUndefined();
    });

    it('rejects an ambiguous channel when multiple endpoints exist', async () => {
        const SocketA = definePondEndpoint('/ambiguous-a');
        const SocketB = definePondEndpoint('/ambiguous-b');
        const Chat = definePondChannel(schema, '/ambiguous-chat');

        @SocketA.Endpoint()
        class EndpointA {}

        @SocketB.Endpoint()
        class EndpointB {}

        @Chat.Channel()
        class ChatChannel {}

        const { service } = createService([
            provider(EndpointA, {}),
            provider(EndpointB, {}),
            provider(ChatChannel, {}),
        ]);

        await expect(service.onModuleInit()).rejects.toThrow(/cannot be associated with an endpoint/);
        await service.onModuleDestroy();
    });

    it('fails fast when a class declares multiple join handlers', async () => {
        const Socket = definePondEndpoint('/duplicates');
        const Chat = definePondChannel(schema, '/duplicates-chat');

        @Socket.Endpoint()
        class SocketEndpoint {}

        @Chat.Channel({ endpoint: SocketEndpoint })
        class ChatChannel {
            @Chat.OnJoin()
            first () {}

            @Chat.OnJoin()
            second () {}
        }

        const { server, service } = createService([
            provider(SocketEndpoint, {}),
            provider(ChatChannel, {}),
        ]);

        await expect(service.onModuleInit()).rejects.toThrow(/multiple join handlers/);
        expect(service.pondSocket).toBeNull();
        expect(server.listenerCount('upgrade')).toBe(0);
        await service.onModuleDestroy();
    });

    it('rejects request-scoped endpoint and channel providers', async () => {
        const Socket = definePondEndpoint('/request-scoped');

        @Socket.Endpoint()
        class SocketEndpoint {}

        const wrapper = {
            ...provider(SocketEndpoint, {}),
            isDependencyTreeStatic: () => false,
        };
        const { service } = createService([wrapper]);

        await expect(service.onModuleInit()).rejects.toThrow(/must be a singleton provider/);
        expect(service.pondSocket).toBeNull();
    });

    it('rejects transient providers even when their dependency tree is static', async () => {
        const Socket = definePondEndpoint('/transient');

        @Socket.Endpoint()
        class SocketEndpoint {}

        const wrapper = {
            ...provider(SocketEndpoint, {}),
            scope: Scope.TRANSIENT,
            isDependencyTreeStatic: () => true,
        };
        const { service } = createService([wrapper]);

        await expect(service.onModuleInit()).rejects.toThrow(/must be a singleton provider/);
        expect(service.pondSocket).toBeNull();
    });

    it('rejects PondSocket providers without a singleton instance', async () => {
        const Socket = definePondEndpoint('/missing-instance');

        @Socket.Endpoint()
        class SocketEndpoint {}

        const wrapper = {
            host: {},
            instance: null,
            metatype: SocketEndpoint,
            name: SocketEndpoint.name,
            isDependencyTreeStatic: () => true,
        };
        const { service } = createService([wrapper]);

        await expect(service.onModuleInit()).rejects.toThrow(/not instantiated as a singleton/);
        expect(service.pondSocket).toBeNull();
    });

    it('rejects multiple live Nest instances of the same PondSocket provider', async () => {
        const Socket = definePondEndpoint('/duplicate-provider');

        @Socket.Endpoint()
        class SocketEndpoint {}

        const { service } = createService([
            provider(SocketEndpoint, { metatype: class FirstModule {} }),
            provider(SocketEndpoint, { metatype: class SecondModule {} }),
        ]);

        await expect(service.onModuleInit()).rejects.toThrow(/multiple Nest provider instances/);
        expect(service.pondSocket).toBeNull();
    });
});
