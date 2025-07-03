import { ClientActions, PondMessage } from '@eleven-am/pondsocket-common';

export interface ClientMessage {
    action: ClientActions;
    event: string;
    payload: PondMessage;
    channelName: string;
    requestId: string;
}

export type Publisher = (data: ClientMessage) => void;
