import { ClientMessage } from '@eleven-am/pondsocket-common';

import { BaseClient } from './baseClient';
import { ConnectionState, TransportPublishError, utf8ByteLength } from '../types';

const SOCKET_CONNECTING = 0;
const SOCKET_OPEN = 1;

export abstract class WebSocketClient extends BaseClient {
    protected _socket: WebSocket | undefined;

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
            if (this._socket === socket && this._connectionState.value !== ConnectionState.CONNECTED) {
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

            // Browser WebSocket APIs do not expose ping/pong control frames.
            // Liveness is therefore owned by the server transport rather than
            // inferred from application-message silence.
        };

        socket.onmessage = (message) => {
            if (this._socket !== socket) {
                return;
            }

            if (typeof message.data !== 'string') {
                this._errorSubject.publish(new Error('WebSocket protocol requires UTF-8 text messages'));
                socket.close(1003, 'Text messages required');

                return;
            }

            if (!this._acceptInboundData(message.data)) {
                socket.close(1009, 'Message too large');

                return;
            }

            const events = this._parseMessages(message.data);

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
            this._connectionState.publish(ConnectionState.DISCONNECTED);
            this._scheduleReconnect();
        };

        this._socket = socket;
    }

    public disconnect () {
        this._clearConnectionTimeout();
        this._clearReconnectTimeout();
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
        return (message: ClientMessage, serialized?: { data: string; byteLength: number }) => {
            const socket = this._socket;

            if (this._connectionState.value !== ConnectionState.CONNECTED || !socket || socket.readyState !== SOCKET_OPEN) {
                throw new TransportPublishError('Cannot send message while the WebSocket is not connected', true);
            }

            let data: string;

            try {
                data = serialized?.data ?? JSON.stringify(message);
            } catch (error) {
                throw new TransportPublishError('Message cannot be serialized as JSON', false, error);
            }
            const byteLength = serialized?.byteLength ?? utf8ByteLength(data);

            if (byteLength > this._options.maxMessageSize) {
                throw new TransportPublishError(
                    `Outbound message exceeds maximum size of ${this._options.maxMessageSize} bytes`,
                    false,
                );
            }
            if (byteLength > this._options.maxBufferedAmount) {
                throw new TransportPublishError(
                    `Outbound message exceeds WebSocket buffer limit of ${this._options.maxBufferedAmount} bytes`,
                    false,
                );
            }
            if (socket.bufferedAmount + byteLength > this._options.maxBufferedAmount) {
                socket.close(1013, 'Backpressure limit exceeded');
                throw new TransportPublishError(
                    `WebSocket buffered data would exceed ${this._options.maxBufferedAmount} bytes`,
                    true,
                );
            }

            try {
                socket.send(data);
            } catch (error) {
                socket.close(1013, 'Send failed');
                throw new TransportPublishError('WebSocket send failed before delivery', true, error);
            }
        };
    }
}
