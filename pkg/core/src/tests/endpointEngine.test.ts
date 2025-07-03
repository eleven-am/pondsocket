import {
    ChannelEvent,
    ClientActions,
    ClientMessage,
    PondPath,
    ServerActions,
    SystemSender,
} from '@eleven-am/pondsocket-common';

import { SocketCache } from '../abstracts/types';
import { JoinContext } from '../contexts/joinContext';
import { EndpointEngine } from '../engines/endpointEngine';
import { PondChannel } from '../wrappers/pondChannel';
import { MockWebSocket } from './mocks/mockWebSocket';

describe('EndpointEngine', () => {
    let endpointEngine: EndpointEngine;

    beforeEach(() => {
        endpointEngine = new EndpointEngine('/test-channel', null);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    describe('createChannel', () => {
        it('should create a new channel and return a PondChannel', () => {
            const path = '/test-channel' as PondPath<'/test-channel'>;
            const handler = jest.fn((request, response) => {
                response.accept();
            });

            const pondChannel = endpointEngine.createChannel(path, handler);

            expect(pondChannel).toBeInstanceOf(PondChannel);
        });

        it('should handle join requests for the created channel', async () => {
            const path = '/test-channel' as PondPath<'/test-channel'>;
            const handler = jest.fn((ctx: JoinContext<any>) => {
                ctx.accept();
            });

            endpointEngine.createChannel(path, handler);

            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            // Simulate a join request
            const joinMessage: ClientMessage = {
                event: 'join',
                action: ClientActions.JOIN_CHANNEL,
                channelName: '/test-channel',
                requestId: '123',
                payload: {},
            };

            // Mock the socket's 'message' event
            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            messageHandler(JSON.stringify(joinMessage));

            // Wait for async operations to complete
            await new Promise((resolve) => setTimeout(resolve, 0));

            // Verify that the handler was called
            expect(handler).toHaveBeenCalled();

            // test the handler's arguments
            expect(handler.mock.calls[0][0]).toBeInstanceOf(JoinContext);

            // Verify that a response was sent back to the client
            expect(mockSocket.send).toHaveBeenCalledWith(expect.stringContaining('"event":"CONNECTION"'));
        });

        it('should handle multiple channels', () => {
            const path1 = '/channel1' as PondPath<'/channel1'>;
            const path2 = '/channel2' as PondPath<'/channel2'>;
            const handler1 = jest.fn();
            const handler2 = jest.fn();

            const channel1 = endpointEngine.createChannel(path1, handler1);
            const channel2 = endpointEngine.createChannel(path2, handler2);

            expect(channel1).toBeInstanceOf(PondChannel);
            expect(channel2).toBeInstanceOf(PondChannel);
        });
    });

    describe('getClients', () => {
        it('should return all connected clients', () => {
            const mockSocket1 = new MockWebSocket();
            const mockSocket2 = new MockWebSocket();
            const socketCache1: SocketCache = {
                clientId: '1',
                socket: mockSocket1 as any,
                assigns: {},
                subscriptions: new Set(),
            };
            const socketCache2: SocketCache = {
                clientId: '2',
                socket: mockSocket2 as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache1);
            endpointEngine.manageSocket(socketCache2);

            const clients = endpointEngine.getClients();

            expect(clients).toHaveLength(2);
            expect(clients).toContainEqual(socketCache1);
            expect(clients).toContainEqual(socketCache2);
        });

        it('should return an empty array when no clients are connected', () => {
            const clients = endpointEngine.getClients();

            expect(clients).toHaveLength(0);
        });
    });

    describe('getUser', () => {
        it('should return the user for a given clientId', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            const user = endpointEngine.getUser('1');

            expect(user).toEqual(socketCache);
        });

        it('should throw an error if the user is not found', () => {
            expect(() => endpointEngine.getUser('nonexistent')).toThrow('GatewayEngine: User nonexistent does not exist');
        });
    });

    describe('manageSocket', () => {
        it('should set up event listeners and send connection event', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            expect(mockSocket.on).toHaveBeenCalledWith('message', expect.any(Function));
            expect(mockSocket.on).toHaveBeenCalledWith('close', expect.any(Function));
            expect(mockSocket.on).toHaveBeenCalledWith('error', expect.any(Function));
            expect(mockSocket.send).toHaveBeenCalledWith(expect.stringContaining('"event":"CONNECTION"'));
        });

        it('should add the socket to the managed sockets', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            expect(endpointEngine.getUser('1')).toEqual(socketCache);
        });
    });


    describe('sendMessage', () => {
        it('should send a message to the given socket', () => {
            const mockSocket = new MockWebSocket();
            const message: ChannelEvent = {
                event: 'test',
                action: ServerActions.BROADCAST,
                channelName: SystemSender.ENDPOINT,
                requestId: '123',
                payload: {},
            };

            endpointEngine.sendMessage(mockSocket as any, message);

            expect(mockSocket.send).toHaveBeenCalledWith(JSON.stringify(message));
        });
    });

    describe('closeConnection', () => {
        it('should close the connection for a single clientId', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            endpointEngine.closeConnection('1');

            expect(mockSocket.close).toHaveBeenCalled();
        });

        it('should close the connection for multiple clientIds', () => {
            const mockSocket1 = new MockWebSocket();
            const mockSocket2 = new MockWebSocket();
            const socketCache1: SocketCache = {
                clientId: '1',
                socket: mockSocket1 as any,
                assigns: {},
                subscriptions: new Set(),
            };

            const socketCache2: SocketCache = {
                clientId: '2',
                socket: mockSocket2 as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache1);
            endpointEngine.manageSocket(socketCache2);

            endpointEngine.closeConnection(['1', '2']);

            expect(mockSocket1.close).toHaveBeenCalled();
            expect(mockSocket2.close).toHaveBeenCalled();
        });

        it('should not throw an error when trying to close a non-existent connection', () => {
            expect(() => endpointEngine.closeConnection('nonexistent')).not.toThrow();
        });
    });

    // Test for private method behavior
    describe('handleSocketClose', () => {
        it('should remove the socket from managed sockets and call all subscriptions', () => {
            const mockSocket = new MockWebSocket();
            const mockUnsubscribe = jest.fn();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set([mockUnsubscribe]),
            };

            endpointEngine.manageSocket(socketCache);

            // Simulate socket close event
            mockSocket.on.mock.calls.find((call) => call[0] === 'close')[1]();

            expect(() => endpointEngine.getUser('1')).toThrow();
            expect(mockUnsubscribe).toHaveBeenCalled();
        });
    });
});
