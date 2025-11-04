import {ChannelEvent, channelEventSchema} from '@eleven-am/pondsocket-common';

import { PondClient as PondSocketClient } from '../browser/client';

// eslint-disable-next-line @typescript-eslint/no-var-requires,@typescript-eslint/no-require-imports
const WebSocket = require('websocket').w3cwebsocket as typeof import('websocket').w3cwebsocket;

export class PondClient extends PondSocketClient {
    /**
     * @desc Connects to the server and returns the socket.
     */
    public connect (backoff = 1) {
        this._disconnecting = false;

        const socket = new WebSocket(this._address.toString());

        socket.onopen = () => this._connectionState.publish(true);

        socket.onmessage = (message) => {
            const lines = (message.data as string).trim().split('\n');

            for (const line of lines) {
                if (line.trim()) {
                    const data = JSON.parse(line);
                    const event = channelEventSchema.parse(data);

                    this._broadcaster.publish(event);
                }
            }
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
