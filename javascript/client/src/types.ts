import { ClientActions, PondMessage } from '@eleven-am/pondsocket-common';

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

export interface ClientMessage {
    action: ClientActions;
    event: string;
    payload: PondMessage;
    channelName: string;
    requestId: string;
}

export type Publisher = (data: ClientMessage) => void;
