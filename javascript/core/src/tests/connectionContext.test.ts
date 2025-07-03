import { IncomingMessage, IncomingHttpHeaders } from 'http';
import { Socket } from 'net';

import { PondAssigns, ServerActions, SystemSender, IncomingConnection } from '@eleven-am/pondsocket-common';

import { ConnectionParams, ConnectionResponseOptions, SocketCache } from '../abstracts/types';
import { ConnectionContext } from '../contexts/connectionContext';
import { HttpError } from '../errors/httpError';
import { MockEndpointEngine } from './mocks/endpointEngine';
import { MockWebSocket } from './mocks/mockWebSocket';

class MockWebSocketServer {
    clients = new Set();

    on = jest.fn();

    handleUpgrade = jest.fn((req, socket, head, cb) => cb());

    emit = jest.fn();
}

describe('ConnectionContext', () => {
    let connectionContext: ConnectionContext<string>;
    let mockEngine: MockEndpointEngine;
    let mockWebSocketServer: MockWebSocketServer;
    let mockParams: ConnectionParams;
    let mockOptions: ConnectionResponseOptions;
    let mockConnectionData: IncomingConnection<string>;

    beforeEach(() => {
        mockEngine = new MockEndpointEngine();
        mockWebSocketServer = new MockWebSocketServer();
        mockParams = {
            request: {} as IncomingMessage,
            socket: new Socket(),
            head: Buffer.from([]),
            requestId: 'request-123',
        };
        mockOptions = {
            engine: mockEngine,
            params: mockParams,
            webSocketServer: mockWebSocketServer as any,
        };
        mockConnectionData = {
            id: 'client-123',
            headers: { 'user-agent': 'test-agent' } as IncomingHttpHeaders,
            address: '127.0.0.1',
            cookies: { session: 'test-session' },
            query: { page: '1' },
            params: { id: '123' },
        };
        connectionContext = new ConnectionContext(mockConnectionData, mockOptions);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    describe('getters', () => {
        it('should return the connection data', () => {
            expect(connectionContext.connection).toEqual(mockConnectionData);
        });

        it('should return the client ID', () => {
            expect(connectionContext.clientId).toBe('client-123');
        });

        it('should return the request ID', () => {
            expect(connectionContext.requestId).toBe('request-123');
        });

        it('should return the headers', () => {
            expect(connectionContext.headers).toEqual({ 'user-agent': 'test-agent' });
        });

        it('should return the cookies', () => {
            expect(connectionContext.cookies).toEqual({ session: 'test-session' });
        });

        it('should return the address', () => {
            expect(connectionContext.address).toBe('127.0.0.1');
        });

        it('should return the event parameters', () => {
            expect(connectionContext.event).toEqual({
                query: { page: '1' },
                params: { id: '123' },
            });
        });

        it('should return the query parameters', () => {
            expect(connectionContext.query).toEqual({ page: '1' });
        });

        it('should return the path parameters', () => {
            expect(connectionContext.params).toEqual({ id: '123' });
        });
    });

    describe('assign', () => {
        it('should update assigns before execution', () => {
            const assigns: PondAssigns = { role: 'user' };

            connectionContext.assign(assigns);
            connectionContext.accept();

            expect(mockWebSocketServer.handleUpgrade).toHaveBeenCalled();
            expect(mockEngine.manageSocket).toHaveBeenCalledWith(expect.objectContaining({
                assigns: { role: 'user' },
            }));
        });

        it('should update user assigns after execution', () => {
            const assigns: PondAssigns = { role: 'user' };

            connectionContext.accept();
            connectionContext.assign(assigns);
            expect(mockEngine.getUser).toHaveBeenCalledWith('client-123');
            expect(mockEngine.getUser('client-123').assigns).toEqual(assigns);
        });
    });

    describe('accept', () => {
        it('should handle upgrade and manage socket', () => {
            const handleUpgradeSpy = jest.spyOn(mockWebSocketServer, 'handleUpgrade');

            connectionContext.accept();
            expect(handleUpgradeSpy).toHaveBeenCalled();
            expect(mockEngine.manageSocket).toHaveBeenCalled();
        });

        it('should throw error if already executed', () => {
            connectionContext.accept();
            expect(() => connectionContext.accept()).toThrow(HttpError);
        });
    });

    describe('decline', () => {
        it('should write error message and destroy socket', () => {
            const writeSpy = jest.spyOn(mockParams.socket, 'write');
            const destroySpy = jest.spyOn(mockParams.socket, 'destroy');

            connectionContext.decline('Unauthorized', 401);
            expect(writeSpy).toHaveBeenCalledWith('HTTP/1.1 401 Unauthorized\r\n\r\n');
            expect(destroySpy).toHaveBeenCalled();
        });

        it('should throw error if already executed', () => {
            connectionContext.decline();
            expect(() => connectionContext.decline()).toThrow(HttpError);
        });
    });

    describe('reply', () => {
        it('should send message to client', () => {
            const mockSocket = new MockWebSocket();

            mockEngine.getUser.mockReturnValue({ socket: mockSocket } as unknown as SocketCache);
            connectionContext.accept();
            connectionContext.reply('test-event', { message: 'Hello' });
            expect(mockEngine.sendMessage).toHaveBeenCalledWith(
                mockSocket,
                expect.objectContaining({
                    event: 'test-event',
                    payload: { message: 'Hello' },
                    action: ServerActions.BROADCAST,
                    channelName: SystemSender.ENDPOINT,
                }),
            );
        });

        it('should throw error if not accepted', () => {
            expect(() => connectionContext.reply('test-event', {})).toThrow(HttpError);
        });
    });

    describe('hasResponded', () => {
        it('should return false initially', () => {
            expect(connectionContext.hasResponded).toBe(false);
        });

        it('should return true after accept', () => {
            connectionContext.accept();
            expect(connectionContext.hasResponded).toBe(true);
        });

        it('should return true after decline', () => {
            connectionContext.decline();
            expect(connectionContext.hasResponded).toBe(true);
        });
    });
});
