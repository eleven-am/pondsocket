import WebSocket from 'ws';

import { WebSocketClient } from '../core/webSocketClient';

export class PondClient extends WebSocketClient {
    protected _createSocket (address: string): globalThis.WebSocket {
        return new WebSocket(address) as unknown as globalThis.WebSocket;
    }

    protected _resolveAddress (endpoint: string, params: Record<string, any>): URL {
        const address = new URL(endpoint);
        const query = new URLSearchParams(params);

        address.search = query.toString();
        const protocol = address.protocol === 'https:' ? 'wss:' : 'ws:';

        if (address.protocol !== 'wss:' && address.protocol !== 'ws:') {
            address.protocol = protocol;
        }

        return address;
    }
}
