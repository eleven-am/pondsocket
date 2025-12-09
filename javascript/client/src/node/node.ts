import { channelEventSchema } from '@eleven-am/pondsocket-common';

import { PondClient as PondSocketClient } from '../browser/client';
import { ConnectionState } from '../types';

// eslint-disable-next-line @typescript-eslint/no-var-requires,@typescript-eslint/no-require-imports
const WebSocket = require('websocket').w3cwebsocket as typeof import('websocket').w3cwebsocket;

export class PondClient extends PondSocketClient {
    /**
     * @desc Connects to the server and returns the socket.
     */
    public connect () {
        this._disconnecting = false;
        this._connectionState.publish(ConnectionState.CONNECTING);

        const socket = new WebSocket(this._address.toString());

        this._connectionTimeoutId = setTimeout(() => {
            if (socket.readyState === WebSocket.CONNECTING) {
                const error = new Error('Connection timeout');

                this._errorSubject.publish(error);
                socket.close();
            }
        }, this._options.connectionTimeout);

        socket.onopen = () => {
            this.#clearTimeouts();
            this._reconnectAttempts = 0;

            if (this._options.pingInterval) {
                this._pingIntervalId = setInterval(() => {
                    if (socket.readyState === WebSocket.OPEN) {
                        socket.send(JSON.stringify({ action: 'ping' }));
                    }
                }, this._options.pingInterval);
            }
        };

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

        socket.onerror = () => {
            const error = new Error('WebSocket error');

            this._errorSubject.publish(error);
            socket.close();
        };

        socket.onclose = () => {
            this.#clearTimeouts();
            this._connectionState.publish(ConnectionState.DISCONNECTED);

            if (this._disconnecting) {
                return;
            }

            const delay = Math.min(
                1000 * 2 ** this._reconnectAttempts,
                this._options.maxReconnectDelay,
            );

            this._reconnectAttempts++;

            setTimeout(() => {
                this.connect();
            }, delay);
        };

        this._socket = socket;
    }

    #clearTimeouts () {
        if (this._connectionTimeoutId) {
            clearTimeout(this._connectionTimeoutId);
            this._connectionTimeoutId = undefined;
        }

        if (this._pingIntervalId) {
            clearInterval(this._pingIntervalId);
            this._pingIntervalId = undefined;
        }
    }
}
