import { WebSocketClient } from '../core/webSocketClient';

export class PondClient extends WebSocketClient {
    protected _resolveAddress (endpoint: string, params: Record<string, any>): URL {
        let address: URL;

        try {
            address = new URL(endpoint);
        } catch (e) {
            address = new URL(window.location.toString());
            address.pathname = endpoint;
            address.hash = '';
        }

        const query = new URLSearchParams(params);

        address.search = query.toString();
        const protocol = address.protocol === 'https:' ? 'wss:' : 'ws:';

        if (address.protocol !== 'wss:' && address.protocol !== 'ws:') {
            address.protocol = protocol;
        }

        return address;
    }
}
