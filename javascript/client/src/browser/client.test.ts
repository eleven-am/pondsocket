import {
    ChannelEvent,
    ChannelState,
    ClientActions,
    definePondChannel,
    definePondSchema,
    ErrorTypes,
    Events,
    PondSchema,
    ServerActions,
} from '@eleven-am/pondsocket-common';

import { PondClient } from './client';
import { Channel } from '../core/channel';
import { ConnectionState } from '../types';

class MockWebSocket {
    static CONNECTING = 0;

    static OPEN = 1;

    static CLOSED = 3;

    send: (...args: unknown[]) => unknown = jest.fn();

    close: (...args: unknown[]) => unknown = jest.fn();

    readyState = MockWebSocket.CONNECTING;

    constructor (readonly url: string) {}
}

// @ts-expect-error the test installs a deliberately incomplete WebSocket implementation
global.WebSocket = MockWebSocket;

describe('PondClient', () => {
    let pondClient: PondClient;

    beforeEach(() => {
        pondClient = new PondClient('ws://example.com');
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    test('connect method should set up WebSocket events', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket']!;

        expect(mockWebSocket.onmessage).toBeInstanceOf(Function);
        expect(mockWebSocket.onclose).toBeInstanceOf(Function);
    });

    test('it should publish messages received from the server', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;
        const broadcasterSpy = jest.spyOn(pondClient['_broadcaster'], 'publish');
        const errorCallback = jest.fn();

        pondClient.onError(errorCallback);

        mockWebSocket.onmessage({
            data: JSON.stringify({
                action: ServerActions.SYSTEM,
                channelName: 'exampleChannel',
                requestId: '123',
                payload: {},
                event: 'exampleEvent',
            }),
        });

        expect(broadcasterSpy).toHaveBeenCalledTimes(1);
        expect(broadcasterSpy).toHaveBeenCalledWith({
            action: ServerActions.SYSTEM,
            channelName: 'exampleChannel',
            requestId: '123',
            payload: {},
            event: 'exampleEvent',
        });

        broadcasterSpy.mockClear();

        expect(() => mockWebSocket.onmessage({ data: 'invalid json' })).not.toThrow();

        expect(broadcasterSpy).not.toHaveBeenCalled();
        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({
            message: expect.stringContaining('Failed to parse server message at line 1'),
        }));

        broadcasterSpy.mockClear();

        expect(() => mockWebSocket.onmessage({ data: JSON.stringify({}) })).not.toThrow();

        expect(broadcasterSpy).not.toHaveBeenCalled();
    });

    test('continues parsing newline-delimited batches after a malformed line', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;
        const broadcasterSpy = jest.spyOn(pondClient['_broadcaster'], 'publish');
        const errorCallback = jest.fn();
        const event = {
            action: ServerActions.SYSTEM,
            channelName: 'exampleChannel',
            requestId: '123',
            payload: {},
            event: 'exampleEvent',
        };

        pondClient.onError(errorCallback);
        mockWebSocket.onmessage({
            data: `${JSON.stringify(event)}\r\nnot-json\n${JSON.stringify({ ...event, requestId: '456' })}\n`,
        });

        expect(broadcasterSpy).toHaveBeenCalledTimes(2);
        expect(broadcasterSpy.mock.calls.map(([message]) => message.requestId)).toEqual(['123', '456']);
        expect(errorCallback).toHaveBeenCalledTimes(1);
    });

    test('does not create overlapping sockets and ignores stale socket callbacks', () => {
        pondClient.connect();
        const firstSocket = pondClient['_socket']! as any;
        const broadcasterSpy = jest.spyOn(pondClient['_broadcaster'], 'publish');

        pondClient.connect();
        expect(pondClient['_socket']).toBe(firstSocket);

        firstSocket.readyState = MockWebSocket.CLOSED;
        pondClient.connect();
        const secondSocket = pondClient['_socket']! as any;
        const event = JSON.stringify({
            action: ServerActions.SYSTEM,
            channelName: 'exampleChannel',
            requestId: '123',
            payload: {},
            event: 'exampleEvent',
        });

        expect(secondSocket).not.toBe(firstSocket);
        expect(firstSocket.close).toHaveBeenCalled();
        firstSocket.onmessage({ data: event });
        expect(broadcasterSpy).not.toHaveBeenCalled();
        secondSocket.onmessage({ data: event });
        expect(broadcasterSpy).toHaveBeenCalledTimes(1);
    });

    test('socket should only pass to publish state when acknowledged event is received', () => {
        const mockCallback = jest.fn();

        pondClient.onConnectionChange(mockCallback);
        mockCallback.mockClear();

        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;

        expect(mockCallback).toHaveBeenCalledWith(ConnectionState.CONNECTING);

        const acknowledgeEvent: ChannelEvent = {
            event: Events.CONNECTION,
            action: ServerActions.CONNECT,
            channelName: 'exampleChannel',
            requestId: '123',
            payload: {},
        };

        mockWebSocket.onmessage({ data: JSON.stringify(acknowledgeEvent) });
        expect(mockCallback).toHaveBeenCalledWith(ConnectionState.CONNECTED);
    });

    test('only resets reconnect backoff after the protocol connection acknowledgement', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;

        pondClient['_reconnectAttempts'] = 4;
        mockWebSocket.onopen();
        expect(pondClient['_reconnectAttempts']).toBe(4);

        mockWebSocket.onmessage({
            data: JSON.stringify({
                event: Events.CONNECTION,
                action: ServerActions.CONNECT,
                channelName: 'ENDPOINT',
                requestId: 'connection',
                payload: {},
            }),
        });

        expect(pondClient['_reconnectAttempts']).toBe(0);
    });

    test('adds jitter to reconnect delays', () => {
        jest.useFakeTimers();
        const randomSpy = jest.spyOn(Math, 'random').mockReturnValue(0);
        const connectSpy = jest.spyOn(pondClient, 'connect');

        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;

        connectSpy.mockClear();
        mockWebSocket.onclose();
        jest.advanceTimersByTime(499);
        expect(connectSpy).not.toHaveBeenCalled();
        jest.advanceTimersByTime(1);
        expect(connectSpy).toHaveBeenCalledTimes(1);

        randomSpy.mockRestore();
        jest.useRealTimers();
    });

    test('createChannel method should create a new channel or return an existing one', () => {
        const mockChannel = pondClient.createChannel('exampleChannel');
        const mockExistingChannel = pondClient.createChannel('exampleChannel');

        expect(mockChannel).toBeInstanceOf(Channel);
        expect(mockExistingChannel).toBe(mockChannel);
    });

    test('createChannel builds a channel name from a shared definition', () => {
        interface Schema extends PondSchema<{ message: { text: string } }, {}, {}, { token: string }> {}

        const schema = definePondSchema<Schema>();
        const definition = definePondChannel(schema, '/rooms/:roomId');
        const channel = pondClient.createChannel(definition, {
            params: {
                roomId: 'general',
            },
            joinParams: {
                token: 'secret',
            },
        });

        expect(channel).toBe(pondClient.createChannel('/rooms/general', { token: 'secret' }));
    });

    test('channel-scoped errors decline the pending join', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;
        const channel = pondClient.createChannel('missing');

        mockWebSocket.readyState = MockWebSocket.OPEN;
        mockWebSocket.onmessage({
            data: JSON.stringify({
                event: Events.CONNECTION,
                action: ServerActions.CONNECT,
                channelName: 'ENDPOINT',
                requestId: 'connection',
                payload: {},
            }),
        });
        channel.join();
        const joinRequest = JSON.parse(mockWebSocket.send.mock.calls[0][0]);

        mockWebSocket.onmessage({
            data: JSON.stringify({
                event: ErrorTypes.CHANNEL_NOT_FOUND,
                action: ServerActions.ERROR,
                channelName: 'missing',
                requestId: joinRequest.requestId,
                payload: {
                    code: ErrorTypes.CHANNEL_NOT_FOUND,
                    message: 'Missing channel',
                    status: 404,
                },
            }),
        });

        expect(channel.channelState).toBe(ChannelState.DECLINED);
        expect(channel.joinError?.message).toBe('Missing channel');
    });

    test('onConnectionChange method should subscribe to connection state changes', () => {
        const mockCallback = jest.fn();
        const unsubscribe = pondClient.onConnectionChange(mockCallback);

        mockCallback.mockClear();

        pondClient['_connectionState'].publish(ConnectionState.CONNECTED);

        expect(mockCallback).toHaveBeenCalledWith(ConnectionState.CONNECTED);

        unsubscribe();

        pondClient['_connectionState'].publish(ConnectionState.DISCONNECTED);

        expect(mockCallback).toHaveBeenCalledTimes(1);
    });

    test('getState method should return the current state of the socket', () => {
        const initialState = pondClient.getState();

        expect(initialState).toBe(ConnectionState.DISCONNECTED);

        pondClient['_connectionState'].publish(ConnectionState.CONNECTED);

        const updatedState = pondClient.getState();

        expect(updatedState).toBe(ConnectionState.CONNECTED);
    });

    test('publish method should send a message to the server', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;
        const channel = pondClient.createChannel('exampleChannel');

        const acknowledgeEvent: ChannelEvent = {
            event: Events.CONNECTION,
            action: ServerActions.CONNECT,
            channelName: 'exampleChannel',
            requestId: '123',
            payload: {},
        };

        mockWebSocket.readyState = MockWebSocket.OPEN;
        mockWebSocket.onmessage({ data: JSON.stringify(acknowledgeEvent) });

        channel.join();

        expect(mockWebSocket.send).toHaveBeenCalledTimes(1);

        const sentObject = mockWebSocket.send.mock.calls[0][0];

        expect(sentObject).toBeDefined();

        expect(JSON.parse(sentObject)).toEqual(
            expect.objectContaining({
                action: ClientActions.JOIN_CHANNEL,
                event: ClientActions.JOIN_CHANNEL,
                payload: {},
                channelName: 'exampleChannel',
            }),
        );
    });

    it('should reconnect with exponential backoff', async () => {
        const connectSpy = jest.spyOn(pondClient, 'connect');

        pondClient.connect();
        let mockWebSocket = pondClient['_socket']! as any;

        expect(connectSpy).toHaveBeenCalledTimes(1);

        connectSpy.mockClear();
        mockWebSocket.onclose();
        await new Promise((resolve) => setTimeout(resolve, 1000));
        expect(connectSpy).toHaveBeenCalledTimes(1);
        connectSpy.mockClear();
        mockWebSocket = pondClient['_socket']! as any;

        mockWebSocket.onclose();

        await new Promise((resolve) => setTimeout(resolve, 2000));
        expect(connectSpy).toHaveBeenCalledTimes(1);
    });

    test('onError method should subscribe to error events', () => {
        const errorCallback = jest.fn();

        pondClient.onError(errorCallback);
        pondClient.connect();

        const mockWebSocket = pondClient['_socket']! as any;

        mockWebSocket.onerror({ type: 'error' });

        expect(errorCallback).toHaveBeenCalledTimes(1);
        expect(errorCallback).toHaveBeenCalledWith(expect.any(Error));
    });

    test('connect should transition through connection states', () => {
        const stateCallback = jest.fn();

        pondClient.onConnectionChange(stateCallback);

        expect(pondClient.getState()).toBe(ConnectionState.DISCONNECTED);

        pondClient.connect();

        expect(stateCallback).toHaveBeenCalledWith(ConnectionState.CONNECTING);
    });

    test('connection timeout should trigger error and close', async () => {
        jest.useFakeTimers();
        const clientWithTimeout = new PondClient('ws://example.com', {}, { connectionTimeout: 5000 });
        const errorCallback = jest.fn();

        clientWithTimeout.onError(errorCallback);
        clientWithTimeout.connect();

        const mockWebSocket = clientWithTimeout['_socket']! as any;

        mockWebSocket.readyState = MockWebSocket.CONNECTING;

        jest.advanceTimersByTime(5000);

        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({ message: 'Connection timeout' }));
        expect(mockWebSocket.close).toHaveBeenCalled();

        jest.useRealTimers();
    });

    test('ping interval should close when no server messages arrive', async () => {
        jest.useFakeTimers();
        const clientWithPing = new PondClient('ws://example.com', {}, { pingInterval: 1000 });
        const errorCallback = jest.fn();

        clientWithPing.onError(errorCallback);
        clientWithPing.connect();

        const mockWebSocket = clientWithPing['_socket']! as any;

        mockWebSocket.readyState = MockWebSocket.OPEN;

        mockWebSocket.onopen();

        jest.advanceTimersByTime(2000);

        expect(mockWebSocket.send).not.toHaveBeenCalledWith(JSON.stringify({ action: 'ping' }));
        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({
            message: 'Connection lost: no response from server',
        }));
        expect(mockWebSocket.close).toHaveBeenCalled();
        jest.useRealTimers();
    });

    test('disconnect should clear timeouts and stop reconnection', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket']! as any;

        pondClient.disconnect();

        expect(pondClient.getState()).toBe(ConnectionState.DISCONNECTED);
        expect(mockWebSocket.close).toHaveBeenCalled();

        const connectSpy = jest.spyOn(pondClient, 'connect');

        mockWebSocket.onclose();

        expect(connectSpy).not.toHaveBeenCalled();
    });
});
