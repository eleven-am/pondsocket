import { createServer } from 'http';
import { EventEmitter } from 'node:events';
import { WebSocketServer } from 'ws';

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

    it('removes every listener it owns from a shared HTTP server across recreations', async () => {
        const server = createServer();
        const baseline = {
            close: server.listenerCount('close'),
            error: server.listenerCount('error'),
            upgrade: server.listenerCount('upgrade'),
        };

        for (let index = 0; index < 2; index++) {
            const socket = new PondSocket({
                closeHttpServerOnShutdown: false,
                server,
            });

            expect(server.listenerCount('close')).toBe(baseline.close + 1);
            expect(server.listenerCount('error')).toBe(baseline.error + 1);
            expect(server.listenerCount('upgrade')).toBe(baseline.upgrade + 1);

            await socket.closeAsync(50);

            expect(server.listenerCount('close')).toBe(baseline.close);
            expect(server.listenerCount('error')).toBe(baseline.error);
            expect(server.listenerCount('upgrade')).toBe(baseline.upgrade);
        }
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

    it('does not wait for backend readiness before starting cleanup', async () => {
        const backend: IDistributedBackend = {
            nodeId: 'hung-node',
            heartbeatTimeoutMs: 1_000,
            initialize: jest.fn(() => new Promise(() => {})),
            broadcast: jest.fn(),
            subscribeToChannel: jest.fn(),
            subscribeToHeartbeats: jest.fn(),
            cleanup: jest.fn().mockResolvedValue(undefined),
        };
        const pond = new PondSocket({ distributedBackend: backend });

        await pond.closeAsync(50);

        expect(backend.cleanup).toHaveBeenCalledTimes(1);
    });

    it('tracks distributed backend disconnects and recovery after startup', async () => {
        let backendHealthy = true;
        const backend: IDistributedBackend = {
            nodeId: 'health-node',
            heartbeatTimeoutMs: 90_000,
            initialize: jest.fn().mockResolvedValue(undefined),
            isHealthy: jest.fn(() => backendHealthy),
            broadcast: jest.fn(),
            subscribeToChannel: jest.fn(),
            subscribeToHeartbeats: jest.fn(),
            cleanup: jest.fn().mockResolvedValue(undefined),
        };
        const socket = new PondSocket({ distributedBackend: backend });

        await socket.ready();
        expect(socket.isHealthy()).toBe(true);

        backendHealthy = false;
        expect(socket.isHealthy()).toBe(false);

        backendHealthy = true;
        expect(socket.isHealthy()).toBe(true);

        await socket.closeAsync(50);
        expect(socket.isHealthy()).toBe(false);
    });

    it('does not expose asynchronous connection-handler failures in the HTTP response', async () => {
        const server = createServer();
        const onError = jest.fn();
        const pond = new PondSocket({ server,
            closeHttpServerOnShutdown: false,
            onError });
        const rawSocket = {
            write: jest.fn(),
            destroy: jest.fn(),
        };

        pond.createEndpoint('/test', async () => {
            throw new Error('database password leaked');
        });
        server.emit('upgrade', { headers: {},
            url: '/test' }, rawSocket, Buffer.alloc(0));
        await new Promise(process.nextTick);

        expect(rawSocket.write).toHaveBeenCalledWith(expect.stringContaining('Internal server error'));
        expect(rawSocket.write.mock.calls[0][0]).not.toContain('database password leaked');
        expect(onError).toHaveBeenCalledWith(expect.objectContaining({
            message: expect.stringContaining('database password leaked'),
        }));

        await pond.closeAsync(50);
    });

    it('destroys an upgrade when connection authorization hangs', async () => {
        jest.useFakeTimers();
        const server = createServer();
        const pond = new PondSocket({ server,
            closeHttpServerOnShutdown: false,
            connectionTimeout: 100 });
        const rawSocket = {
            write: jest.fn(),
            destroy: jest.fn(),
        };

        pond.createEndpoint('/test', () => new Promise(() => {}));
        server.emit('upgrade', { headers: {},
            url: '/test' }, rawSocket, Buffer.alloc(0));
        jest.advanceTimersByTime(100);

        expect(rawSocket.write).toHaveBeenCalledWith(expect.stringContaining('HTTP/1.1 408 Request Timeout'));
        expect(rawSocket.destroy).toHaveBeenCalledTimes(1);

        jest.useRealTimers();
        await pond.closeAsync(50);
    });

    it('clears the authorization timeout after a handler declines', async () => {
        jest.useFakeTimers();
        const server = createServer();
        const pond = new PondSocket({ server,
            closeHttpServerOnShutdown: false,
            connectionTimeout: 100 });
        const rawSocket = {
            write: jest.fn(),
            destroy: jest.fn(),
        };

        pond.createEndpoint('/test', (context) => context.decline('Denied', 401));
        server.emit('upgrade', { headers: {},
            url: '/test' }, rawSocket, Buffer.alloc(0));
        jest.advanceTimersByTime(100);

        expect(rawSocket.write).toHaveBeenCalledTimes(1);
        expect(rawSocket.write).toHaveBeenCalledWith(expect.stringContaining('HTTP/1.1 401 Unauthorized'));
        expect(rawSocket.destroy).toHaveBeenCalledTimes(1);

        jest.useRealTimers();
        await pond.closeAsync(50);
    });

    it('relinquishes unmatched upgrades without timing them out in non-exclusive mode', async () => {
        jest.useFakeTimers();
        const server = createServer();
        const pond = new PondSocket({ server,
            closeHttpServerOnShutdown: false,
            connectionTimeout: 100,
            exclusiveServer: false });
        const rawSocket = {
            write: jest.fn(),
            destroy: jest.fn(),
        };

        pond.createEndpoint('/owned', (context) => context.accept());
        server.emit('upgrade', { headers: {},
            url: '/owned-by-another-listener' }, rawSocket, Buffer.alloc(0));
        jest.advanceTimersByTime(100);

        expect(rawSocket.write).not.toHaveBeenCalled();
        expect(rawSocket.destroy).not.toHaveBeenCalled();

        jest.useRealTimers();
        await pond.closeAsync(50);
    });

    it('parses prototype-like cookie names as ordinary own properties', async () => {
        const server = createServer();
        const pond = new PondSocket({ server,
            closeHttpServerOnShutdown: false });
        const rawSocket = { write: jest.fn(),
            destroy: jest.fn() };
        let cookies: Record<string, string> | undefined;

        pond.createEndpoint('/test', (context) => {
            cookies = context.cookies;
            context.decline('done');
        });
        server.emit('upgrade', {
            headers: { cookie: '__proto__=polluted; constructor=value' },
            url: '/test',
        }, rawSocket, Buffer.alloc(0));
        await new Promise(process.nextTick);

        expect(Object.prototype.hasOwnProperty.call(cookies, '__proto__')).toBe(true);
        expect(cookies?.__proto__).toBe('polluted');
        expect(cookies?.constructor).toBe('value');
        expect(Object.getPrototypeOf(cookies!)).toBe(Object.prototype);

        await pond.closeAsync(50);
    });

    it('does not upgrade when the raw peer closes during authorization', async () => {
        let release!: () => void;
        const gate = new Promise<void>((resolve) => release = resolve);
        const server = createServer();
        const socketServer = new WebSocketServer({ noServer: true });
        const handleUpgrade = jest.spyOn(socketServer, 'handleUpgrade');
        const pond = new PondSocket({ server,
            socketServer,
            closeHttpServerOnShutdown: false });
        const rawSocket = Object.assign(new EventEmitter(), {
            write: jest.fn(),
            destroy: jest.fn(),
        });

        pond.createEndpoint('/test', async (context) => {
            await gate;
            expect(() => context.accept()).toThrow(/expired/);
        });
        server.emit('upgrade', { headers: {},
            url: '/test' }, rawSocket, Buffer.alloc(0));
        rawSocket.emit('close');
        release();
        await new Promise(setImmediate);

        expect(handleUpgrade).not.toHaveBeenCalled();
        await pond.closeAsync(50);
    });
});
