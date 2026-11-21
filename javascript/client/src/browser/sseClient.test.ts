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
        (global.fetch as jest.Mock).mockReset().mockResolvedValue({
            ok: true,
            status: 202,
        });
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

    test('rejects an invalid connection id instead of entering a false connected state', () => {
        const errorCallback = jest.fn();

        sseClient.onError(errorCallback);
        sseClient.connect();
        const mockEventSource = sseClient['_eventSource'] as unknown as MockEventSource;

        mockEventSource.onmessage?.({
            data: JSON.stringify({
                event: Events.CONNECTION,
                action: ServerActions.CONNECT,
                channelName: 'GATEWAY',
                requestId: '123',
                payload: { connectionId: 42 },
            }),
        } as MessageEvent);

        expect(sseClient.getConnectionId()).toBeUndefined();
        expect(sseClient.getState()).toBe(ConnectionState.DISCONNECTED);
        expect(mockEventSource.close).toHaveBeenCalled();
        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({
            message: expect.stringContaining('invalid connection id'),
        }));
        sseClient.disconnect();
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
        const firstDelivery = publisher(firstMessage);
        const secondDelivery = publisher(secondMessage);
        await Promise.resolve();

        expect(fetchMock).toHaveBeenCalledTimes(1);
        expect(JSON.parse(fetchMock.mock.calls[0][1].body).requestId).toBe('first');

        resolveFirst?.({ ok: true, status: 202 } as Response);
        await Promise.all([firstDelivery, secondDelivery]);

        expect(fetchMock).toHaveBeenCalledTimes(2);
        expect(fetchMock.mock.calls.map(([, options]) => JSON.parse(options.body).requestId)).toEqual(['first', 'second']);
    });

    test('bounds the pending HTTP request queue synchronously', async () => {
        const boundedClient = new SSEClient('http://example.com/events', {}, { maxPendingRequests: 1 });
        const errorCallback = jest.fn();
        const publisher = boundedClient['_createPublisher']();
        const message = {
            action: ClientActions.BROADCAST,
            channelName: 'testChannel',
            requestId: 'first',
            event: 'first',
            payload: {},
        };

        boundedClient.onError(errorCallback);
        boundedClient['_connectionId'] = 'test-conn-id';
        boundedClient['_connectionState'].publish(ConnectionState.CONNECTED);
        const firstDelivery = publisher(message);

        expect(() => publisher({ ...message, requestId: 'second' })).toThrow(/queue capacity/);
        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({
            message: expect.stringContaining('queue capacity'),
        }));
        await firstDelivery;
        boundedClient.disconnect();
    });

    test('cancels in-flight and queued writes when the connection generation changes', async () => {
        const errorCallback = jest.fn();
        const fetchMock = global.fetch as jest.Mock;

        fetchMock.mockImplementation((_url: string, options: RequestInit) => new Promise((_resolve, reject) => {
            options.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
        }));
        sseClient.onError(errorCallback);
        sseClient.connect();
        const eventSource = sseClient['_eventSource'] as unknown as MockEventSource;
        const publisher = sseClient['_createPublisher']();
        const message = {
            action: ClientActions.BROADCAST,
            channelName: 'testChannel',
            requestId: 'first',
            event: 'first',
            payload: {},
        };

        sseClient['_connectionId'] = 'test-conn-id';
        sseClient['_connectionState'].publish(ConnectionState.CONNECTED);
        const firstDelivery = publisher(message);
        const secondDelivery = publisher({ ...message, requestId: 'second' });
        await Promise.resolve();

        eventSource.onerror?.(new Event('error'));
        await sseClient['_publishChain'];
        await Promise.allSettled([firstDelivery, secondDelivery]);

        expect(fetchMock).toHaveBeenCalledTimes(1);
        expect(errorCallback.mock.calls.some(([error]) => error.message.includes('connection changed'))).toBe(true);
        sseClient.disconnect();
    });

    test('aborts a hung HTTP write after requestTimeout', async () => {
        jest.useFakeTimers();
        const timedClient = new SSEClient('http://example.com/events', {}, { requestTimeout: 50 });
        const errorCallback = jest.fn();

        (global.fetch as jest.Mock).mockImplementation((_url: string, options: RequestInit) =>
            new Promise((_resolve, reject) => {
                options.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
            }));
        timedClient.onError(errorCallback);
        timedClient['_connectionId'] = 'test-conn-id';
        timedClient['_connectionState'].publish(ConnectionState.CONNECTED);
        const delivery = timedClient['_createPublisher']()({
            action: ClientActions.BROADCAST,
            channelName: 'testChannel',
            requestId: 'first',
            event: 'first',
            payload: {},
        });
        const rejectedDelivery = expect(delivery).rejects.toThrow(/delivery failed/);
        await Promise.resolve();
        await jest.advanceTimersByTimeAsync(50);
        await timedClient['_publishChain'];
        await rejectedDelivery;

        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({
            message: expect.stringContaining('timed out after 50ms'),
        }));
        timedClient.disconnect();
        jest.useRealTimers();
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

    test('connection timeout remains armed after EventSource open until CONNECTION arrives', () => {
        jest.useFakeTimers();
        const clientWithTimeout = new SSEClient('http://example.com/events', {}, { connectionTimeout: 5000 });
        const errorCallback = jest.fn();

        clientWithTimeout.onError(errorCallback);
        clientWithTimeout.connect();
        const eventSource = clientWithTimeout['_eventSource'] as unknown as MockEventSource;

        eventSource.readyState = MockEventSource.OPEN;
        eventSource.onopen?.(new Event('open'));
        jest.advanceTimersByTime(5000);

        expect(errorCallback).toHaveBeenCalledWith(expect.objectContaining({ message: 'Connection timeout' }));
        expect(eventSource.close).toHaveBeenCalled();
        expect(clientWithTimeout.getState()).toBe(ConnectionState.DISCONNECTED);
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

    test('rejects ambiguous HTTP failures, closes the stream, and never replays the request', async () => {
        (global.fetch as jest.Mock).mockResolvedValueOnce({ ok: false, status: 503 });
        sseClient.connect();
        const eventSource = sseClient['_eventSource'] as unknown as MockEventSource;

        sseClient['_connectionId'] = 'test-conn-id';
        sseClient['_connectionState'].publish(ConnectionState.CONNECTED);
        const delivery = sseClient['_createPublisher']()({
            action: ClientActions.BROADCAST,
            channelName: 'testChannel',
            requestId: 'ambiguous',
            event: 'event',
            payload: {},
        });

        await expect(delivery).rejects.toThrow(/not replayed/);
        expect(eventSource.close).toHaveBeenCalled();
        expect(sseClient.getState()).toBe(ConnectionState.DISCONNECTED);
        expect(global.fetch).toHaveBeenCalledTimes(1);
        sseClient.disconnect();
    });

    test('fails immediately with a useful error when EventSource is unavailable', () => {
        const eventSource = global.EventSource;

        // @ts-expect-error simulate the Node.js runtime
        delete global.EventSource;
        expect(() => new SSEClient('http://example.com/events')).toThrow(/requires the browser EventSource API/);
        global.EventSource = eventSource;
    });
});
