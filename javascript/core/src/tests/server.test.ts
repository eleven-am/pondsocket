import { createServer } from 'http';

import { IDistributedBackend, PondSocket } from '..';

describe('PondSocket lifecycle', () => {
    it('does not close a shared HTTP server when ownership is disabled', async () => {
        const server = createServer();
        const socket = new PondSocket({
            closeHttpServerOnShutdown: false,
            server,
        });

        await new Promise<void>((resolve) => server.listen(0, resolve));

        expect(server.listenerCount('upgrade')).toBe(1);

        await socket.closeAsync(50);

        expect(server.listening).toBe(true);
        expect(server.listenerCount('upgrade')).toBe(0);

        await new Promise<void>((resolve, reject) => {
            server.close((error) => error ? reject(error) : resolve());
        });
    });

    it('reports backend initialization failures through readiness and health', async () => {
        const initializationError = new Error('backend unavailable');
        const backend: IDistributedBackend = {
            nodeId: 'test-node',
            initialize: jest.fn().mockRejectedValue(initializationError),
            broadcast: jest.fn(),
            subscribeToChannel: jest.fn(),
            subscribeToHeartbeats: jest.fn(),
            cleanup: jest.fn(),
        };
        const socket = new PondSocket({ distributedBackend: backend });

        await expect(socket.ready()).rejects.toBe(initializationError);
        expect(socket.isHealthy()).toBe(false);

        await socket.closeAsync(50);
        expect(backend.cleanup).toHaveBeenCalledTimes(1);
    });
});
