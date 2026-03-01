import { ClientMessage } from '@eleven-am/pondsocket-common';

export { ClientMessage };

export enum ConnectionState {
    DISCONNECTED = 'disconnected',
    CONNECTING = 'connecting',
    CONNECTED = 'connected',
}

export interface ClientOptions {
    connectionTimeout?: number;
    maxReconnectDelay?: number;
    pingInterval?: number;
}

export type Publisher = (data: ClientMessage) => void;
