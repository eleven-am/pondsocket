import 'reflect-metadata';
import { createServer } from 'http';

import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
    PondSchema,
} from '@eleven-am/pondsocket';

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

        const { service } = createService([
            provider(SocketEndpoint, {}),
            provider(ChatChannel, {}),
        ]);

        await expect(service.onModuleInit()).rejects.toThrow(/multiple join handlers/);
        await service.onModuleDestroy();
    });
});
