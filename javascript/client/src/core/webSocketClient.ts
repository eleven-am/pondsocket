import { ClientMessage } from '@eleven-am/pondsocket-common';

import { BaseClient } from './baseClient';
import { ConnectionState } from '../types';

export abstract class WebSocketClient extends BaseClient {
    protected _socket: WebSocket | undefined;

    protected _pingIntervalId: ReturnType<typeof setInterval> | undefined;

    protected _pongTimeoutId: ReturnType<typeof setTimeout> | undefined;

    protected _lastMessageTime: number = 0;

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
            this._clearConnectionTimeout();
            this._clearPingInterval();
            this._clearPongTimeout();
            this._reconnectAttempts = 0;
            this._lastMessageTime = Date.now();

            if (this._options.pingInterval) {
                this._pingIntervalId = setInterval(() => {
                    if (socket.readyState === WebSocket.OPEN) {
                        socket.send(JSON.stringify({ action: 'ping' }));
                    }
                }, this._options.pingInterval);

                this._schedulePongTimeout(socket);
            }
        };

        socket.onmessage = (message) => {
            this._lastMessageTime = Date.now();
            const events = this._parseMessages(message.data as string);

            for (const event of events) {
                this._broadcaster.publish(event);
            }
        };

        socket.onerror = () => {
            const error = new Error('WebSocket error');

            this._errorSubject.publish(error);
            socket.close();
        };

        socket.onclose = () => {
            this._clearConnectionTimeout();
            this._clearPingInterval();
            this._clearPongTimeout();
            this._connectionState.publish(ConnectionState.DISCONNECTED);
            this._scheduleReconnect();
        };

        this._socket = socket;
    }

    public disconnect () {
        this._clearConnectionTimeout();
        this._clearPingInterval();
        this._clearPongTimeout();
        this._disconnecting = true;
        this._connectionState.publish(ConnectionState.DISCONNECTED);
        this._socket?.close();
        this._clearChannels();
    }

    protected _createPublisher () {
        return (message: ClientMessage) => {
            if (this._connectionState.value === ConnectionState.CONNECTED) {
                this._socket?.send(JSON.stringify(message));
            }
        };
    }

    private _clearPingInterval () {
        if (this._pingIntervalId) {
            clearInterval(this._pingIntervalId);
            this._pingIntervalId = undefined;
        }
    }

    private _clearPongTimeout () {
        if (this._pongTimeoutId) {
            clearTimeout(this._pongTimeoutId);
            this._pongTimeoutId = undefined;
        }
    }

    private _schedulePongTimeout (socket: WebSocket) {
        this._clearPongTimeout();
        const timeout = this._options.pingInterval! * 2;

        this._pongTimeoutId = setTimeout(() => {
            if (Date.now() - this._lastMessageTime >= timeout) {
                this._errorSubject.publish(new Error('Connection lost: no response from server'));
                socket.close();

                return;
            }

            this._schedulePongTimeout(socket);
        }, timeout);
    }
}
