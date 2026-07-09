import {
    definePondChannel,
    definePondEndpoint,
    definePondSchema,
    getPondMetadata,
    pondChannelMetadataKey,
    pondEndpointMetadataKey,
    pondHandlerMetadataKey,
    pondParameterMetadataKey,
    PondChannelClassMetadata,
    PondHandlerDefinition,
    PondParameterDefinition,
} from './definitions';
import { PondSchema } from './types';

type ChatSchema = PondSchema<{
    message: { text: string };
    '/history/:messageId': [{ includeMetadata: boolean }, { text: string }];
}, { online: boolean }, { role: string }, { token: string }>;

describe('Pond definitions', () => {
    const schema = definePondSchema<ChatSchema>();
    const endpoint = definePondEndpoint('/v1/socket');
    const channel = definePondChannel(schema, '/chat/:roomId');

    it('builds channel names without repeating the path type', () => {
        expect(channel.build({ roomId: 'general' })).toBe('/chat/general');
    });

    it('stores endpoint and channel metadata without a Nest dependency', () => {
        @endpoint.Endpoint()
        class SocketEndpoint {}

        @channel.Channel({ endpoint: SocketEndpoint })
        class ChatController {}

        expect(getPondMetadata(SocketEndpoint, pondEndpointMetadataKey)).toEqual({
            definition: endpoint,
        });
        expect(getPondMetadata<PondChannelClassMetadata>(ChatController, pondChannelMetadataKey)).toEqual({
            definition: channel,
            options: {
                endpoint: SocketEndpoint,
            },
        });
    });

    it('stores typed handler metadata', () => {
        class ChatController {
            @channel.OnEvent('message')
            message () {}
        }

        expect(getPondMetadata<PondHandlerDefinition[]>(ChatController.prototype, pondHandlerMetadataKey)).toEqual([
            expect.objectContaining({
                definition: channel,
                event: 'message',
                lifecycle: 'event',
                propertyKey: 'message',
            }),
        ]);
    });

    it('stores definition-bound context parameter metadata', () => {
        class ChatController {
            @channel.OnEvent('message')
            message (@channel.GetContext() _context: unknown) {}
        }

        expect(getPondMetadata<PondParameterDefinition[]>(ChatController.prototype, pondParameterMetadataKey)).toEqual([
            {
                definition: channel,
                index: 0,
                propertyKey: 'message',
                source: 'context',
            },
        ]);
    });

    it('stores endpoint-bound context parameter metadata', () => {
        class SocketEndpoint {
            @endpoint.OnConnection()
            connect (@endpoint.GetContext() _context: unknown) {}
        }

        expect(getPondMetadata<PondParameterDefinition[]>(SocketEndpoint.prototype, pondParameterMetadataKey)).toEqual([
            {
                definition: endpoint,
                index: 0,
                propertyKey: 'connect',
                source: 'context',
            },
        ]);
    });
});
