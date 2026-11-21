import { ClientMessage } from '@eleven-am/pondsocket-common';

export { ClientMessage };

export enum ConnectionState {
    DISCONNECTED = 'disconnected',
    CONNECTING = 'connecting',
    CONNECTED = 'connected',
}

export interface ClientOptions {
    connectionTimeout?: number;
    joinTimeout?: number;
    maxReconnectDelay?: number;
    /**
     * @deprecated Browser WebSocket APIs do not expose control-frame pongs.
     * This option is retained for source compatibility but does not infer
     * liveness from application-message silence.
     */
    pingInterval?: number;
    maxQueueSize?: number;
    maxQueueBytes?: number;
    maxChannels?: number;
    maxMessageSize?: number;
    maxBufferedAmount?: number;
    maxPendingRequests?: number;
    requestTimeout?: number;
}

export class TransportPublishError extends Error {
    public readonly retryable: boolean;

    constructor (message: string, retryable: boolean, cause?: unknown) {
        super(message);
        this.name = 'TransportPublishError';
        this.retryable = retryable;
        if (cause !== undefined) {
            (this as Error & { cause?: unknown }).cause = cause;
        }
    }
}

export type SerializedClientMessage = { data: string; byteLength: number };

export type Publisher = (data: ClientMessage, serialized?: SerializedClientMessage) => void | Promise<void>;

export const utf8ByteLength = (value: string) => {
    let bytes = 0;

    for (let index = 0; index < value.length; index++) {
        const code = value.charCodeAt(index);

        if (code < 0x80) {
            bytes++;
        } else if (code < 0x800) {
            bytes += 2;
        } else if (code >= 0xD800 && code <= 0xDBFF && index + 1 < value.length &&
            value.charCodeAt(index + 1) >= 0xDC00 && value.charCodeAt(index + 1) <= 0xDFFF) {
            bytes += 4;
            index++;
        } else {
            bytes += 3;
        }
    }

    return bytes;
};
