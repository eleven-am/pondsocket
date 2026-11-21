import {
    ChannelEvent,
    ClientActions,
    ClientMessage,
    ErrorTypes,
    PondPath,
    ServerActions,
    SystemSender,
} from '@eleven-am/pondsocket-common';

import { SocketCache } from '../abstracts/types';
import { JoinContext } from '../contexts/joinContext';
import { EndpointEngine } from '../engines/endpointEngine';
import { HttpError } from '../errors/httpError';
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
            expect(mockSocket.send).toHaveBeenCalledWith(expect.stringContaining('"event":"CONNECTION"'), expect.any(Function));
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

        it('removes a kicked registration before the same socket rejoins', async () => {
            const pondChannel = endpointEngine.createChannel('/rooms/:id', (context) => context.accept());
            const mockSocket = new MockWebSocket();
            const cache: SocketCache = {
                clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
                channelSubscriptions: new Map(),
            };

            endpointEngine.manageSocket(cache);
            const receive = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];
            const join = (requestId: string) => receive(JSON.stringify({
                event: 'join',
                action: ClientActions.JOIN_CHANNEL,
                channelName: '/rooms/1',
                requestId,
                payload: {},
            }));

            join('request-1');
            await new Promise(setImmediate);

            expect(cache.subscriptions.size).toBe(1);
            expect(cache.channelSubscriptions?.size).toBe(1);

            pondChannel.getChannel('/rooms/1')!.evictUser('client1', 'retry');

            expect(cache.subscriptions.size).toBe(0);
            expect(cache.channelSubscriptions?.size).toBe(0);

            join('request-2');
            await new Promise(setImmediate);

            expect(cache.subscriptions.size).toBe(1);
            expect(cache.channelSubscriptions?.size).toBe(1);
        });

        it('treats leave for an absent channel membership as idempotent', async () => {
            endpointEngine.createChannel('/room', (context) => context.accept());
            const mockSocket = new MockWebSocket();

            endpointEngine.manageSocket({
                clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
                channelSubscriptions: new Map(),
            });
            mockSocket.send.mockClear();
            const receive = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            receive(JSON.stringify({
                event: 'leave',
                action: ClientActions.LEAVE_CHANNEL,
                channelName: '/room',
                requestId: 'request-1',
                payload: {},
            }));
            await new Promise(setImmediate);

            expect(mockSocket.send).not.toHaveBeenCalled();
        });

        it('removes an unused concrete channel after a join is declined', async () => {
            const pondChannel = endpointEngine.createChannel('/rooms/:id', (context) => context.decline('Denied'));
            const mockSocket = new MockWebSocket();

            endpointEngine.manageSocket({
                clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            });
            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            messageHandler(JSON.stringify({
                event: 'join',
                action: ClientActions.JOIN_CHANNEL,
                channelName: '/rooms/1',
                requestId: 'request-1',
                payload: {},
            }));
            await new Promise(setImmediate);

            expect(pondChannel.getChannel('/rooms/1')).toBeNull();
        });

        it('should reject unsupported channel patterns during registration', () => {
            expect(() => endpointEngine.createChannel('job:*', jest.fn())).toThrow(/wildcards must occupy an entire path segment/);
        });
    });

    describe('join authorization bounds', () => {
        it('times out a hanging join and removes its unused concrete channel', async () => {
            jest.useFakeTimers();
            const engine = new EndpointEngine('/test', null, 1024, 4096, null, 100);
            const pondChannel = engine.createChannel('/rooms/:id', () => new Promise(() => {}));
            const mockSocket = new MockWebSocket();

            engine.manageSocket({
                clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            });
            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            messageHandler(JSON.stringify({
                event: 'join',
                action: ClientActions.JOIN_CHANNEL,
                channelName: '/rooms/1',
                requestId: 'request-1',
                payload: {},
            }));
            await jest.advanceTimersByTimeAsync(100);

            const response = JSON.parse(mockSocket.send.mock.calls[mockSocket.send.mock.calls.length - 1][0]);

            expect(response.payload.status).toBe(408);
            expect(pondChannel.getChannel('/rooms/1')).toBeNull();

            engine.close();
            jest.useRealTimers();
        });

        it('prevents a deferred join handler from accepting after socket cleanup', async () => {
            let release!: () => void;
            const gate = new Promise<void>((resolve) => release = resolve);
            let lateError: unknown;
            const engine = new EndpointEngine('/test', null);
            const pondChannel = engine.createChannel('/rooms/:id', async (context) => {
                await gate;
                try {
                    context.accept();
                } catch (error) {
                    lateError = error;
                }
            });
            const mockSocket = new MockWebSocket();
            const cache: SocketCache = { clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set() };

            engine.manageSocket(cache);
            const receive = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];
            const close = mockSocket.on.mock.calls.find((call) => call[0] === 'close')[1];

            receive(JSON.stringify({ action: ClientActions.JOIN_CHANNEL,
                event: 'join',
                channelName: '/rooms/1',
                requestId: 'request-1',
                payload: {} }));
            await Promise.resolve();
            close();
            release();
            await new Promise(setImmediate);

            expect(lateError).toBeInstanceOf(HttpError);
            expect(cache.subscriptions.size).toBe(0);
            expect(pondChannel.getChannel('/rooms/1')).toBeNull();
            engine.close();
        });

    });

    describe('inbound message queue', () => {
        it('processes broadcasts from one socket in protocol order', async () => {
            let release!: () => void;
            const gate = new Promise<void>((resolve) => release = resolve);
            const order: string[] = [];
            const engine = new EndpointEngine('/test', null);
            const channel = engine.createChannel('/room', (context) => context.accept());
            const mockSocket = new MockWebSocket();

            channel.onEvent('*', async (context) => {
                if (context.event.event === 'first') {
                    await gate;
                }
                order.push(context.event.event);
            });
            engine.manageSocket({ clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set() });
            const receive = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];
            const message = (action: ClientActions, event: string) => JSON.stringify({
                action,
                event,
                channelName: '/room',
                requestId: event,
                payload: {},
            });

            receive(message(ClientActions.JOIN_CHANNEL, 'join'));
            receive(message(ClientActions.BROADCAST, 'first'));
            receive(message(ClientActions.BROADCAST, 'second'));
            await new Promise(setImmediate);

            expect(order).toEqual([]);

            release();
            await new Promise(setImmediate);

            expect(order).toEqual(['first', 'second']);
            engine.close();
        });

        it('closes a socket when its bounded inbound queue overflows', async () => {
            const gate = new Promise<void>(() => {});
            const handlerStarts = jest.fn();
            const engine = new EndpointEngine('/test', null, 1024, 4096, null, 10_000, 32, 256, 4096, 2, 4096, 10_000);
            const channel = engine.createChannel('/room', (context) => context.accept());
            const mockSocket = new MockWebSocket();

            channel.onEvent('*', async () => {
                handlerStarts();
                await gate;
            });
            engine.manageSocket({ clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set() });
            const receive = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];
            const message = (action: ClientActions, event: string) => JSON.stringify({ action,
                event,
                channelName: '/room',
                requestId: event,
                payload: {} });

            receive(message(ClientActions.JOIN_CHANNEL, 'join'));
            await new Promise(setImmediate);
            receive(message(ClientActions.BROADCAST, 'first'));
            receive(message(ClientActions.BROADCAST, 'second'));
            receive(message(ClientActions.BROADCAST, 'third'));
            receive(message(ClientActions.BROADCAST, 'after-overflow'));
            await new Promise(setImmediate);

            expect(mockSocket.close).toHaveBeenCalledWith(1009, 'Inbound message backlog exceeded');
            expect(mockSocket.close).toHaveBeenCalledTimes(1);
            expect(handlerStarts).toHaveBeenCalledTimes(1);
            engine.close();
        });

        it('closes a socket when an event handler exceeds its deadline', async () => {
            jest.useFakeTimers();
            const handlerStarts = jest.fn();
            const engine = new EndpointEngine('/test', null, 1024, 4096, null, 10_000, 32, 256, 4096, 64, 4096, 100);
            const channel = engine.createChannel('/room', (context) => context.accept());
            const mockSocket = new MockWebSocket();

            channel.onEvent('*', () => {
                handlerStarts();

                return new Promise(() => {});
            });
            engine.manageSocket({ clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set() });
            const receive = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];
            const message = (action: ClientActions, event: string) => JSON.stringify({ action,
                event,
                channelName: '/room',
                requestId: event,
                payload: {} });

            receive(message(ClientActions.JOIN_CHANNEL, 'join'));
            await Promise.resolve();
            await Promise.resolve();
            receive(message(ClientActions.BROADCAST, 'slow'));
            await jest.advanceTimersByTimeAsync(100);
            receive(message(ClientActions.BROADCAST, 'after-timeout'));
            await Promise.resolve();

            expect(mockSocket.close).toHaveBeenCalledWith(1008, 'Message handler timed out');
            expect(handlerStarts).toHaveBeenCalledTimes(1);

            engine.close();
            jest.useRealTimers();
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
            expect(mockSocket.send).toHaveBeenCalledWith(expect.stringContaining('"event":"CONNECTION"'), expect.any(Function));
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

            expect(mockSocket.send).toHaveBeenCalledWith(JSON.stringify(message), expect.any(Function));
        });

        it('rejects a single outbound message that exceeds the byte limit', () => {
            const engine = new EndpointEngine('/test', null, 100, 1_000);
            const mockSocket = new MockWebSocket();
            const message: ChannelEvent = {
                event: 'test',
                action: ServerActions.BROADCAST,
                channelName: SystemSender.ENDPOINT,
                requestId: '123',
                payload: { value: 'é'.repeat(100) },
            };

            engine.sendMessage(mockSocket as any, message);

            expect(mockSocket.send).not.toHaveBeenCalled();
            expect(mockSocket.close).toHaveBeenCalledWith(1009, expect.any(String));
        });

        it('accounts for the new message when enforcing bufferedAmount', () => {
            const engine = new EndpointEngine('/test', null, 1_000, 150);
            const mockSocket = new MockWebSocket();

            mockSocket.bufferedAmount = 100;
            engine.sendMessage(mockSocket as any, {
                event: 'test',
                action: ServerActions.BROADCAST,
                channelName: SystemSender.ENDPOINT,
                requestId: '123',
                payload: { value: 'x'.repeat(100) },
            });

            expect(mockSocket.send).not.toHaveBeenCalled();
            expect(mockSocket.close).toHaveBeenCalledWith(1009, expect.any(String));
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

            const closeHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'close')[1];

            closeHandler();

            expect(() => endpointEngine.getUser('1')).toThrow();
            expect(mockUnsubscribe).toHaveBeenCalled();
        });

        it('should register error handler without removing socket (close event handles cleanup)', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            const errorHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'error')[1];

            errorHandler();

            expect(endpointEngine.getUser('1')).toEqual(socketCache);
        });
    });

    describe('message size limits', () => {
        it('should reject messages exceeding maxMessageSize', () => {
            const engine = new EndpointEngine('/test', null, 1024);
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            engine.manageSocket(socketCache);

            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];
            const longMessage = 'x'.repeat(1500);

            messageHandler(longMessage);

            expect(mockSocket.send).toHaveBeenCalledTimes(2);

            const lastCall = mockSocket.send.mock.calls[1][0];
            const parsed = JSON.parse(lastCall);

            expect(parsed.action).toBe(ServerActions.ERROR);
            expect(parsed.payload.error.status).toBe(413);
        });
    });

    describe('invalid JSON messages', () => {
        it('should send error response for malformed JSON', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            messageHandler('not valid json');

            expect(mockSocket.send).toHaveBeenCalledTimes(2);

            const lastCall = mockSocket.send.mock.calls[1][0];
            const parsed = JSON.parse(lastCall);

            expect(parsed.action).toBe(ServerActions.ERROR);
            expect(parsed.event).toBe('INVALID_MESSAGE');
            expect(parsed.payload.message).toBe('Internal server error');
        });

        it('reports the internal cause without exposing it to the client', () => {
            const onError = jest.fn();
            const engine = new EndpointEngine('/test', null, 1024, 4096, onError);
            const mockSocket = new MockWebSocket();

            engine.manageSocket({
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            });
            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            messageHandler('{"secret":');

            const response = JSON.parse(mockSocket.send.mock.calls[1][0]);

            expect(response.payload.message).toBe('Internal server error');
            expect(response.payload.message).not.toContain('secret');
            expect(onError).toHaveBeenCalledWith(expect.objectContaining({
                message: expect.stringContaining('Internal endpoint error'),
            }));
        });
    });

    describe('invalid client message schema', () => {
        it('should send error response for messages failing schema validation', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: '1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            messageHandler(JSON.stringify({ invalid: 'data' }));

            expect(mockSocket.send).toHaveBeenCalledTimes(2);

            const lastCall = mockSocket.send.mock.calls[1][0];
            const parsed = JSON.parse(lastCall);

            expect(parsed.action).toBe(ServerActions.ERROR);
        });
    });

    describe('leave channel', () => {
        it('should handle LEAVE_CHANNEL action', () => {
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

            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            const joinMessage: ClientMessage = {
                event: 'join',
                action: ClientActions.JOIN_CHANNEL,
                channelName: '/test-channel',
                requestId: '123',
                payload: {},
            };

            messageHandler(JSON.stringify(joinMessage));

            const leaveMessage: ClientMessage = {
                event: 'leave',
                action: ClientActions.LEAVE_CHANNEL,
                channelName: '/test-channel',
                requestId: '456',
                payload: {},
            };

            messageHandler(JSON.stringify(leaveMessage));
        });
    });

    describe('broadcast message', () => {
        it('should handle BROADCAST action', () => {
            const path = '/test-channel' as PondPath<'/test-channel'>;

            endpointEngine.createChannel(path, (ctx: JoinContext<any>) => {
                ctx.accept();
            });

            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            const joinMessage: ClientMessage = {
                event: 'join',
                action: ClientActions.JOIN_CHANNEL,
                channelName: '/test-channel',
                requestId: '123',
                payload: {},
            };

            messageHandler(JSON.stringify(joinMessage));

            const broadcastMessage: ClientMessage = {
                event: 'hello',
                action: ClientActions.BROADCAST,
                channelName: '/test-channel',
                requestId: '789',
                payload: { text: 'hi' },
            };

            messageHandler(JSON.stringify(broadcastMessage));
        });
    });

    describe('join non-existent channel', () => {
        it('should send error when joining a channel with no matching pattern', () => {
            const mockSocket = new MockWebSocket();
            const socketCache: SocketCache = {
                clientId: 'client1',
                socket: mockSocket as any,
                assigns: {},
                subscriptions: new Set(),
            };

            endpointEngine.manageSocket(socketCache);

            const messageHandler = mockSocket.on.mock.calls.find((call) => call[0] === 'message')[1];

            const joinMessage: ClientMessage = {
                event: 'join',
                action: ClientActions.JOIN_CHANNEL,
                channelName: '/nonexistent',
                requestId: '123',
                payload: {},
            };

            messageHandler(JSON.stringify(joinMessage));

            const calls = mockSocket.send.mock.calls;
            const lastCall = JSON.parse(calls[calls.length - 1][0]);

            expect(lastCall.action).toBe(ServerActions.ERROR);
            expect(lastCall.event).toBe(ErrorTypes.CHANNEL_NOT_FOUND);
            expect(lastCall.channelName).toBe('/nonexistent');
            expect(lastCall.requestId).toBe('123');
            expect(lastCall.payload).toEqual({
                code: ErrorTypes.CHANNEL_NOT_FOUND,
                message: 'GatewayEngine: Channel /nonexistent does not exist',
                status: 404,
                statusCode: 404,
                error: {
                    message: 'GatewayEngine: Channel /nonexistent does not exist',
                    status: 404,
                },
            });
        });
    });
});
