import { ChannelEvent, ClientActions, Events, ServerActions } from '@eleven-am/pondsocket-common';

import { PondClient } from './client';
import { ConnectionState } from '../types';

class MockWebSocket {
    static CONNECTING = 0;

    static OPEN = 1;

    send: Function = jest.fn();

    close: Function = jest.fn();

    readyState = MockWebSocket.CONNECTING;

    // eslint-disable-next-line no-useless-constructor
    constructor (readonly url: string) {}
}

// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-expect-error
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
        const mockWebSocket = pondClient['_socket'];

        expect(mockWebSocket.onmessage).toBeInstanceOf(Function);
        expect(mockWebSocket.onclose).toBeInstanceOf(Function);
    });

    test('it should publish messages received from the server', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket'];
        const broadcasterSpy = jest.spyOn(pondClient['_broadcaster'], 'publish');

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

        expect(() => {
            mockWebSocket.onmessage({ data: 'invalid json' });
        }).toThrow();

        expect(broadcasterSpy).not.toHaveBeenCalled();

        broadcasterSpy.mockClear();

        expect(() => {
            mockWebSocket.onmessage({ data: JSON.stringify({}) });
        }).toThrow();

        expect(broadcasterSpy).not.toHaveBeenCalled();
    });

    test('socket should only pass to publish state when acknowledged event is received', () => {
        const mockCallback = jest.fn();

        pondClient.onConnectionChange(mockCallback);
        mockCallback.mockClear();

        pondClient.connect();
        const mockWebSocket = pondClient['_socket'];

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

    test('createChannel method should create a new channel or return an existing one', () => {
        const mockChannel = pondClient.createChannel('exampleChannel');
        const mockExistingChannel = pondClient.createChannel('exampleChannel');

        // eslint-disable-next-line @typescript-eslint/no-var-requires
        expect(mockChannel).toBeInstanceOf(require('../core/channel').Channel);
        expect(mockExistingChannel).toBe(mockChannel);
    });

    test('onConnectionChange method should subscribe to connection state changes', () => {
        const mockCallback = jest.fn();
        const unsubscribe = pondClient.onConnectionChange(mockCallback);

        mockCallback.mockClear();

        pondClient['_connectionState'].publish(ConnectionState.CONNECTED);

        expect(mockCallback).toHaveBeenCalledWith(ConnectionState.CONNECTED);

        unsubscribe();

        pondClient['_connectionState'].publish(ConnectionState.DISCONNECTED);

        // Should not be called again after unsubscribe
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
        const mockWebSocket = pondClient['_socket'];
        const channel = pondClient.createChannel('exampleChannel');

        const acknowledgeEvent: ChannelEvent = {
            event: Events.CONNECTION,
            action: ServerActions.CONNECT,
            channelName: 'exampleChannel',
            requestId: '123',
            payload: {},
        };

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
        let mockWebSocket = pondClient['_socket'];

        expect(connectSpy).toHaveBeenCalledTimes(1);

        connectSpy.mockClear();
        mockWebSocket.onclose();
        await new Promise((resolve) => setTimeout(resolve, 1000));
        expect(connectSpy).toHaveBeenCalledTimes(1);
        connectSpy.mockClear();
        mockWebSocket = pondClient['_socket'];

        mockWebSocket.onclose();

        await new Promise((resolve) => setTimeout(resolve, 2000));
        expect(connectSpy).toHaveBeenCalledTimes(1);
    });

    test('onError method should subscribe to error events', () => {
        const errorCallback = jest.fn();

        pondClient.onError(errorCallback);
        pondClient.connect();

        const mockWebSocket = pondClient['_socket'];

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

        const mockWebSocket = clientWithTimeout['_socket'];

        mockWebSocket.readyState = MockWebSocket.CONNECTING;

        jest.advanceTimersByTime(5000);

        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({ message: 'Connection timeout' }));
        expect(mockWebSocket.close).toHaveBeenCalled();

        jest.useRealTimers();
    });

    test('ping interval should send ping messages when connected', async () => {
        jest.useFakeTimers();
        const clientWithPing = new PondClient('ws://example.com', {}, { pingInterval: 1000 });

        clientWithPing.connect();

        const mockWebSocket = clientWithPing['_socket'];

        mockWebSocket.readyState = MockWebSocket.OPEN;

        mockWebSocket.onopen();

        jest.advanceTimersByTime(1000);

        expect(mockWebSocket.send).toHaveBeenCalledWith(JSON.stringify({ action: 'ping' }));

        jest.advanceTimersByTime(1000);

        expect(mockWebSocket.send).toHaveBeenCalledTimes(2);

        jest.useRealTimers();
    });

    test('disconnect should clear timeouts and stop reconnection', () => {
        pondClient.connect();
        const mockWebSocket = pondClient['_socket'];

        pondClient.disconnect();

        expect(pondClient.getState()).toBe(ConnectionState.DISCONNECTED);
        expect(mockWebSocket.close).toHaveBeenCalled();

        const connectSpy = jest.spyOn(pondClient, 'connect');

        mockWebSocket.onclose();

        expect(connectSpy).not.toHaveBeenCalled();
    });
});
