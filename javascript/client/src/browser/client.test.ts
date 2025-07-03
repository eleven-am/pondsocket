import { ChannelEvent, ClientActions, Events, ServerActions } from '@eleven-am/pondsocket-common';

import PondClient from './client';

class MockWebSocket {
    send: Function = jest.fn();

    close: Function = jest.fn();

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
        pondClient.connect();
        const mockWebSocket = pondClient['_socket'];
        const mockCallback = jest.fn();

        pondClient.onConnectionChange(mockCallback);

        expect(mockCallback).not.toHaveBeenCalled();

        const acknowledgeEvent: ChannelEvent = {
            event: Events.CONNECTION,
            action: ServerActions.CONNECT,
            channelName: 'exampleChannel',
            requestId: '123',
            payload: {},
        };

        mockWebSocket.onmessage({ data: JSON.stringify(acknowledgeEvent) });
        expect(mockCallback).toHaveBeenCalledWith(true);
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

        pondClient['_connectionState'].publish(true);

        expect(mockCallback).toHaveBeenCalledWith(true);

        unsubscribe();

        pondClient['_connectionState'].publish(false);

        // Should not be called again after unsubscribe
        expect(mockCallback).toHaveBeenCalledTimes(1);
    });

    test('getState method should return the current state of the socket', () => {
        const initialState = pondClient.getState();

        expect(initialState).toBe(false);

        pondClient['_connectionState'].publish(true);

        const updatedState = pondClient.getState();

        expect(updatedState).toBe(true);
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

    it('onError method should reconnect to the server', async () => {
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
});
