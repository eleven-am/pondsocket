import { ChannelEvent } from '@eleven-am/pondsocket-common';

import PondSocketClient from '../browser/client';

// eslint-disable-next-line @typescript-eslint/no-var-requires
const WebSocket = require('websocket').w3cwebsocket as typeof import('websocket').w3cwebsocket;

export default class PondClient extends PondSocketClient {
    /**
     * @desc Connects to the server and returns the socket.
     */
    public connect (backoff = 1) {
        this._disconnecting = false;

        const socket = new WebSocket(this._address.toString());

        socket.onopen = () => this._connectionState.publish(true);

        socket.onmessage = (message) => {
            const data = JSON.parse(message.data as string) as ChannelEvent;

            this._broadcaster.publish(data);
        };

        socket.onerror = () => socket.close();

        socket.onclose = () => {
            this._connectionState.publish(false);
            if (this._disconnecting) {
                return;
            }

            setTimeout(() => {
                this.connect();
            }, 1000);
        };
    }
}
