import { ChannelEvent, ClientActions, Events, ServerActions } from '@eleven-am/pondsocket-common';

import { SSEClient } from './sseClient';
import { ConnectionState } from '../types';

class MockEventSource {
    static CONNECTING = 0;

    static OPEN = 1;

    static CLOSED = 2;

    onopen: ((event: Event) => void) | null = null;

    onmessage: ((event: MessageEvent) => void) | null = null;

    onerror: ((event: Event) => void) | null = null;

    readyState: number = MockEventSource.CONNECTING;

    close = jest.fn();

    url: string;

    options?: { withCredentials?: boolean };

    constructor (url: string, options?: { withCredentials?: boolean }) {
        this.url = url;
        this.options = options;
    }
}

// @ts-expect-error the test installs a deliberately incomplete EventSource implementation
global.EventSource = MockEventSource;

global.fetch = jest.fn(() =>
    Promise.resolve({
        ok: true,
        status: 202,
    }),
) as jest.Mock;

describe('SSEClient', () => {
    let sseClient: SSEClient;

    beforeEach(() => {
        sseClient = new SSEClient('http://example.com/events');
        jest.clearAllMocks();
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    test('connect method should set up EventSource events', () => {
        sseClient.connect();
        const mockEventSource = sseClient['_eventSource'];

        expect(mockEventSource?.onmessage).toBeInstanceOf(Function);
        expect(mockEventSource?.onerror).toBeInstanceOf(Function);
        expect(mockEventSource?.onopen).toBeInstanceOf(Function);
    });

    test('does not create overlapping event sources and ignores stale messages', () => {
        sseClient.connect();
        const firstEventSource = sseClient['_eventSource'] as unknown as MockEventSource;

        sseClient.connect();
        expect(sseClient['_eventSource']).toBe(firstEventSource);

        firstEventSource.readyState = MockEventSource.CLOSED;
        sseClient.connect();
        const secondEventSource = sseClient['_eventSource'] as unknown as MockEventSource;
        const connectionEvent = (connectionId: string) => ({
            data: JSON.stringify({
                event: Events.CONNECTION,
                action: ServerActions.CONNECT,
                channelName: 'GATEWAY',
                requestId: connectionId,
                payload: { connectionId },
            }),
        } as MessageEvent);

        expect(secondEventSource).not.toBe(firstEventSource);
        expect(firstEventSource.close).toHaveBeenCalled();
        firstEventSource.onmessage?.(connectionEvent('stale'));
        expect(sseClient.getConnectionId()).toBeUndefined();
        secondEventSource.onmessage?.(connectionEvent('current'));
        expect(sseClient.getConnectionId()).toBe('current');
    });

    test('connect should transition to CONNECTING state', () => {
        const stateCallback = jest.fn();

        sseClient.onConnectionChange(stateCallback);
        stateCallback.mockClear();

        sseClient.connect();

        expect(stateCallback).toHaveBeenCalledWith(ConnectionState.CONNECTING);
    });

    test('it should publish messages received from the server', () => {
        sseClient.connect();
        const mockEventSource = sseClient['_eventSource'];
        const broadcasterSpy = jest.spyOn(sseClient['_broadcaster'], 'publish');

        const event = {
            data: JSON.stringify({
                action: ServerActions.SYSTEM,
                channelName: 'testChannel',
                requestId: '123',
                payload: {},
                event: 'testEvent',
            }),
        };

        mockEventSource?.onmessage?.(event as MessageEvent);

        expect(broadcasterSpy).toHaveBeenCalledTimes(1);
        expect(broadcasterSpy).toHaveBeenCalledWith({
            action: ServerActions.SYSTEM,
            channelName: 'testChannel',
            requestId: '123',
            payload: {},
            event: 'testEvent',
        });
    });

    test('should extract connectionId from CONNECTION event', () => {
        sseClient.connect();
        const mockEventSource = sseClient['_eventSource'];

        const connectEvent: ChannelEvent = {
            event: Events.CONNECTION,
            action: ServerActions.CONNECT,
            channelName: 'GATEWAY',
            requestId: '123',
            payload: { connectionId: 'test-conn-id' },
        };

        mockEventSource?.onmessage?.({ data: JSON.stringify(connectEvent) } as MessageEvent);

        expect(sseClient.getConnectionId()).toBe('test-conn-id');
    });

    test('should transition to CONNECTED state on CONNECTION event', () => {
        const mockCallback = jest.fn();

        sseClient.onConnectionChange(mockCallback);
        mockCallback.mockClear();

        sseClient.connect();
        const mockEventSource = sseClient['_eventSource'];

        const connectEvent: ChannelEvent = {
            event: Events.CONNECTION,
            action: ServerActions.CONNECT,
            channelName: 'GATEWAY',
            requestId: '123',
            payload: { connectionId: 'test-conn-id' },
        };

        mockEventSource?.onmessage?.({ data: JSON.stringify(connectEvent) } as MessageEvent);

        expect(mockCallback).toHaveBeenCalledWith(ConnectionState.CONNECTED);
    });

    test('createChannel method should create a new channel', () => {
        const channel = sseClient.createChannel('testChannel');

        expect(channel).toBeDefined();
        expect(sseClient.createChannel('testChannel')).toBe(channel);
    });

    test('getState method should return the current state', () => {
        expect(sseClient.getState()).toBe(ConnectionState.DISCONNECTED);

        sseClient['_connectionState'].publish(ConnectionState.CONNECTED);

        expect(sseClient.getState()).toBe(ConnectionState.CONNECTED);
    });

    test('onError method should subscribe to error events', () => {
        const errorCallback = jest.fn();

        sseClient.onError(errorCallback);
        sseClient.connect();

        const mockEventSource = sseClient['_eventSource'];

        mockEventSource?.onerror?.(new Event('error'));

        expect(errorCallback).toHaveBeenCalledTimes(1);
        expect(errorCallback).toHaveBeenCalledWith(expect.any(Error));
    });

    test('disconnect should close EventSource and send DELETE request', async () => {
        sseClient.connect();
        const mockEventSource = sseClient['_eventSource'];

        sseClient['_connectionId'] = 'test-conn-id';
        sseClient.disconnect();
        await sseClient['_publishChain'];

        expect(mockEventSource?.close).toHaveBeenCalled();
        expect(sseClient.getState()).toBe(ConnectionState.DISCONNECTED);
        expect(global.fetch).toHaveBeenCalledWith(
            expect.any(String),
            expect.objectContaining({
                method: 'DELETE',
                headers: expect.objectContaining({
                    'X-Connection-ID': 'test-conn-id',
                }),
            }),
        );
    });

    test('should send messages via POST with connection ID', async () => {
        sseClient.connect();
        sseClient['_connectionId'] = 'test-conn-id';
        sseClient['_connectionState'].publish(ConnectionState.CONNECTED);

        const channel = sseClient.createChannel('testChannel');

        const connectEvent: ChannelEvent = {
            event: Events.ACKNOWLEDGE,
            action: ServerActions.SYSTEM,
            channelName: 'testChannel',
            requestId: '123',
            payload: {},
        };

        channel.join();
        sseClient['_broadcaster'].publish(connectEvent);

        channel.sendMessage('testEvent', { data: 'test' });

        await new Promise((resolve) => setTimeout(resolve, 10));

        expect(global.fetch).toHaveBeenCalledWith(
            expect.any(String),
            expect.objectContaining({
                method: 'POST',
                headers: expect.objectContaining({
                    'Content-Type': 'application/json',
                    'X-Connection-ID': 'test-conn-id',
                }),
                body: expect.any(String),
            }),
        );
    });

    test('serializes outgoing requests in publication order', async () => {
        let resolveFirst: ((response: Response) => void) | undefined;
        const firstResponse = new Promise<Response>((resolve) => {
            resolveFirst = resolve;
        });
        const fetchMock = global.fetch as jest.Mock;
        const publisher = sseClient['_createPublisher']();
        const firstMessage = {
            action: ClientActions.BROADCAST,
            channelName: 'testChannel',
            requestId: 'first',
            event: 'first',
            payload: {},
        };
        const secondMessage = {
            ...firstMessage,
            requestId: 'second',
            event: 'second',
        };

        fetchMock
            .mockImplementationOnce(() => firstResponse)
            .mockResolvedValueOnce({ ok: true, status: 202 });
        sseClient['_connectionId'] = 'test-conn-id';
        sseClient['_connectionState'].publish(ConnectionState.CONNECTED);
        publisher(firstMessage);
        publisher(secondMessage);
        await Promise.resolve();

        expect(fetchMock).toHaveBeenCalledTimes(1);
        expect(JSON.parse(fetchMock.mock.calls[0][1].body).requestId).toBe('first');

        resolveFirst?.({ ok: true, status: 202 } as Response);
        await sseClient['_publishChain'];

        expect(fetchMock).toHaveBeenCalledTimes(2);
        expect(fetchMock.mock.calls.map(([, options]) => JSON.parse(options.body).requestId)).toEqual(['first', 'second']);
    });

    test('connection timeout should trigger error', () => {
        jest.useFakeTimers();

        const clientWithTimeout = new SSEClient('http://example.com/events', {}, { connectionTimeout: 5000 });
        const errorCallback = jest.fn();

        clientWithTimeout.onError(errorCallback);
        clientWithTimeout.connect();

        const mockEventSource = clientWithTimeout['_eventSource'] as unknown as MockEventSource;

        mockEventSource.readyState = MockEventSource.CONNECTING;

        jest.advanceTimersByTime(5000);

        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({ message: 'Connection timeout' }));
        expect(mockEventSource.close).toHaveBeenCalled();
        expect(clientWithTimeout.getState()).toBe(ConnectionState.DISCONNECTED);
        expect(clientWithTimeout['_eventSource']).toBeUndefined();

        clientWithTimeout.disconnect();
        jest.useRealTimers();
    });

    test('should reconnect with exponential backoff on error', async () => {
        const connectSpy = jest.spyOn(sseClient, 'connect');

        sseClient.connect();
        const mockEventSource = sseClient['_eventSource'];

        expect(connectSpy).toHaveBeenCalledTimes(1);
        connectSpy.mockClear();

        mockEventSource?.onerror?.(new Event('error'));

        await new Promise((resolve) => setTimeout(resolve, 1100));

        expect(connectSpy).toHaveBeenCalledTimes(1);
    });

    test('should use withCredentials option when set', () => {
        const clientWithCredentials = new SSEClient('http://example.com/events', {}, { withCredentials: true });

        clientWithCredentials.connect();
        const mockEventSource = clientWithCredentials['_eventSource'] as unknown as MockEventSource;

        expect(mockEventSource.options?.withCredentials).toBe(true);
    });
});
