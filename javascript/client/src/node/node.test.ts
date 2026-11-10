import { PondClient } from './node';
import { ConnectionState } from '../types';

describe('PondClient (Node)', () => {
    it('should resolve http endpoint to ws protocol', () => {
        const client = new PondClient('http://localhost:3000/ws', { token: 'abc' });
        const address = (client as any)._address;

        expect(address.protocol).toBe('ws:');
        expect(address.hostname).toBe('localhost');
        expect(address.port).toBe('3000');
        expect(address.pathname).toBe('/ws');
        expect(address.searchParams.get('token')).toBe('abc');
    });

    it('should resolve https endpoint to wss protocol', () => {
        const client = new PondClient('https://example.com/ws', {});
        const address = (client as any)._address;

        expect(address.protocol).toBe('wss:');
        expect(address.hostname).toBe('example.com');
    });

    it('should keep ws protocol if already specified', () => {
        const client = new PondClient('ws://localhost:3000/ws', {});
        const address = (client as any)._address;

        expect(address.protocol).toBe('ws:');
    });

    it('should keep wss protocol if already specified', () => {
        const client = new PondClient('wss://example.com/ws', {});
        const address = (client as any)._address;

        expect(address.protocol).toBe('wss:');
    });

    it('should publish without relying on a global WebSocket implementation', () => {
        const originalWebSocket = globalThis.WebSocket;

        Reflect.deleteProperty(globalThis, 'WebSocket');
        try {
            const client = new PondClient('ws://localhost:3000/ws');
            const send = jest.fn();
            const socket = {
                readyState: 1,
                send,
                close: jest.fn(),
                onopen: null,
                onmessage: null,
                onerror: null,
                onclose: null,
            };

            jest.spyOn(client as any, '_createSocket').mockReturnValue(socket);
            client.connect();
            client['_connectionState'].publish(ConnectionState.CONNECTED);
            client.createChannel('/chat/general').join();

            expect(send).toHaveBeenCalledTimes(1);
        } finally {
            if (originalWebSocket) {
                globalThis.WebSocket = originalWebSocket;
            }
        }
    });
});
