import { PondSocketModule } from '../modules/pondSocket';

const dependencies = [
    { get: jest.fn(), resolve: jest.fn() },
    { httpAdapter: { getHttpServer: jest.fn() } },
    { getControllers: jest.fn(), getProviders: jest.fn() },
] as const;

const createFromModule = async (definition: ReturnType<typeof PondSocketModule.forRoot>) => {
    const provider = definition.providers?.[0] as any;

    return provider.useFactory(...dependencies);
};

describe('PondSocketModule configuration', () => {
    it('owns unmatched upgrades by default and forwards transport limits', async () => {
        const service = await createFromModule(PondSocketModule.forRoot({
            maxMessageSize: 1024,
            maxBufferedAmount: 2048,
            heartbeatInterval: 30_000,
            connectionTimeout: 5_000,
            joinTimeout: 4_000,
            maxPendingJoins: 16,
            maxPendingMessagesPerUser: 128,
            maxPendingBytesPerUser: 16_384,
            maxPendingMessagesPerSocket: 32,
            maxPendingBytesPerSocket: 32_768,
            eventTimeout: 3_000,
        })) as any;

        expect(service.isExclusiveSocketServer).toBe(true);
        expect(service.maxMessageSize).toBe(1024);
        expect(service.maxBufferedAmount).toBe(2048);
        expect(service.heartbeatInterval).toBe(30_000);
        expect(service.connectionTimeout).toBe(5_000);
        expect(service.joinTimeout).toBe(4_000);
        expect(service.maxPendingJoins).toBe(16);
        expect(service.maxPendingMessagesPerUser).toBe(128);
        expect(service.maxPendingBytesPerUser).toBe(16_384);
        expect(service.maxPendingMessagesPerSocket).toBe(32);
        expect(service.maxPendingBytesPerSocket).toBe(32_768);
        expect(service.eventTimeout).toBe(3_000);
    });

    it('forwards limits returned by an async factory', async () => {
        const definition = PondSocketModule.forRootAsync({
            isExclusiveSocketServer: false,
            useFactory: () => ({
                maxMessageSize: 4096,
                maxBufferedAmount: 8192,
                heartbeatInterval: 15_000,
                connectionTimeout: 2_500,
                joinTimeout: 2_000,
                maxPendingJoins: 8,
                maxPendingMessagesPerUser: 64,
                maxPendingBytesPerUser: 8_192,
                maxPendingMessagesPerSocket: 16,
                maxPendingBytesPerSocket: 16_384,
                eventTimeout: 1_500,
            }),
        });
        const service = await createFromModule(definition) as any;

        expect(service.isExclusiveSocketServer).toBe(false);
        expect(service.maxMessageSize).toBe(4096);
        expect(service.maxBufferedAmount).toBe(8192);
        expect(service.heartbeatInterval).toBe(15_000);
        expect(service.connectionTimeout).toBe(2_500);
        expect(service.joinTimeout).toBe(2_000);
        expect(service.maxPendingJoins).toBe(8);
        expect(service.maxPendingMessagesPerUser).toBe(64);
        expect(service.maxPendingBytesPerUser).toBe(8_192);
        expect(service.maxPendingMessagesPerSocket).toBe(16);
        expect(service.maxPendingBytesPerSocket).toBe(16_384);
        expect(service.eventTimeout).toBe(1_500);
    });
});
