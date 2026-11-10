import { ClientMessage } from '@eleven-am/pondsocket-common';

import { BaseClient } from './baseClient';
import { ConnectionState } from '../types';

const SOCKET_CONNECTING = 0;
const SOCKET_OPEN = 1;

export abstract class WebSocketClient extends BaseClient {
    protected _socket: WebSocket | undefined;

    protected _pingIntervalId: ReturnType<typeof setInterval> | undefined;

    protected _pongTimeoutId: ReturnType<typeof setTimeout> | undefined;

    protected _lastMessageTime: number = 0;

    public connect () {
        if (!this._disconnecting && this._socket &&
            (this._socket.readyState === SOCKET_CONNECTING || this._socket.readyState === SOCKET_OPEN)) {
            return;
        }

        this._clearReconnectTimeout();
        this._clearConnectionTimeout();
        this._disconnecting = false;
        this._connectionState.publish(ConnectionState.CONNECTING);

        const previousSocket = this._socket;
        const socket = this._createSocket(this._address.toString());

        this._socket = socket;
        if (previousSocket && previousSocket !== socket) {
            previousSocket.close();
        }

        this._connectionTimeoutId = setTimeout(() => {
            if (this._socket === socket && socket.readyState === SOCKET_CONNECTING) {
                const error = new Error('Connection timeout');

                this._errorSubject.publish(error);
                socket.close();
            }
        }, this._options.connectionTimeout);
        this._unrefTimer(this._connectionTimeoutId);

        socket.onopen = () => {
            if (this._socket !== socket) {
                return;
            }

            this._clearConnectionTimeout();
            this._clearPingInterval();
            this._clearPongTimeout();
            this._lastMessageTime = Date.now();

            if (this._options.pingInterval) {
                this._schedulePongTimeout(socket);
            }
        };

        socket.onmessage = (message) => {
            if (this._socket !== socket) {
                return;
            }

            this._lastMessageTime = Date.now();
            const events = this._parseMessages(String(message.data));

            for (const event of events) {
                this._broadcaster.publish(event);
            }
        };

        socket.onerror = () => {
            if (this._socket !== socket) {
                return;
            }

            const error = new Error('WebSocket error');

            this._errorSubject.publish(error);
            socket.close();
        };

        socket.onclose = () => {
            if (this._socket !== socket) {
                return;
            }

            this._socket = undefined;
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
        this._clearReconnectTimeout();
        this._clearPingInterval();
        this._clearPongTimeout();
        this._disconnecting = true;
        this._connectionState.publish(ConnectionState.DISCONNECTED);
        const socket = this._socket;

        this._socket = undefined;
        socket?.close();
        this._clearChannels();
    }

    protected _createSocket (address: string): WebSocket {
        return new WebSocket(address);
    }

    protected _createPublisher () {
        return (message: ClientMessage) => {
            const socket = this._socket;

            if (this._connectionState.value !== ConnectionState.CONNECTED || !socket || socket.readyState !== SOCKET_OPEN) {
                throw new Error('Cannot send message while the WebSocket is not connected');
            }

            socket.send(JSON.stringify(message));
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
            if (this._socket !== socket) {
                return;
            }

            if (Date.now() - this._lastMessageTime >= timeout) {
                this._errorSubject.publish(new Error('Connection lost: no response from server'));
                socket.close();

                return;
            }

            this._schedulePongTimeout(socket);
        }, timeout);
        this._unrefTimer(this._pongTimeoutId);
    }
}
