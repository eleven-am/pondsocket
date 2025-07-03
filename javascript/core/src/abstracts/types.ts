import { IncomingHttpHeaders } from 'http';
import { IncomingMessage } from 'node:http';
import internal from 'node:stream';

import {
    ChannelEvent,
    EventParams,
    JoinParams,
    PondAssigns,
    PondMessage,
    ServerActions,
    SystemSender,
    Unsubscribe,
    UserData,
} from '@eleven-am/pondsocket-common';
import { WebSocket, WebSocketServer } from 'ws';

import { ConnectionContext } from '../contexts/connectionContext';
import { EventContext } from '../contexts/eventContext';
import { JoinContext } from '../contexts/joinContext';
import { OutgoingContext } from '../contexts/outgoingContext';
import { EndpointEngine } from '../engines/endpointEngine';
import { HttpError } from '../errors/httpError';
import { Channel } from '../wrappers/channel';

export type NextFunction<Error extends HttpError = HttpError> = (error?: Error) => void;

export type MiddlewareFunction<Request, Response> = (req: Request, res: Response, next: NextFunction) => void | Promise<void>;

export interface SocketRequest {
    id: string;
    headers: IncomingHttpHeaders;
    address: string;
}

export type ConnectionHandler<Path extends string> = (ctx: ConnectionContext<Path>, next: NextFunction) => void | Promise<void>;

export type AuthorizationHandler<Path extends string> = (ctx: JoinContext<Path>, next: NextFunction) => void | Promise<void>;

export type EventHandler<Path extends string> = (ctx: EventContext<Path>, next: NextFunction) => void | Promise<void>;

export type OutgoingEventHandler<Event extends string> = (event: OutgoingContext<Event>, next: NextFunction) => PondMessage | Promise<PondMessage> | void | Promise<void>;

export interface ConnectionParams {
    head: Buffer;
    socket: internal.Duplex;
    request: IncomingMessage;
    requestId: string;
}

export interface SocketCache {
    clientId: string;
    socket: WebSocket;
    assigns: PondAssigns;
    subscriptions: Set<Unsubscribe>;
}

export interface ConnectionResponseOptions {
    engine: EndpointEngine;
    params: ConnectionParams;
    webSocketServer: WebSocketServer;
}

export interface JoinRequestOptions<Path extends string> {
    clientId: string;
    assigns: PondAssigns;
    joinParams: JoinParams;
    params: EventParams<Path>;
}

export interface RequestCache extends SocketCache {
    channelName: string;
    requestId: string;
}

export type InternalChannelEvent = ChannelEvent & {
    recipients: string[];
}

export type ChannelSenders = SystemSender.CHANNEL | string;

export type BroadcastEvent = Omit<InternalChannelEvent, 'action' | 'payload' | 'recipients'> & {
    action: ServerActions.BROADCAST;
    sender: ChannelSenders;
    payload: PondMessage;
}

export interface LeaveEvent {
    user: UserData;
    channel: Channel;
}

export type LeaveCallback = (event: LeaveEvent) => void;

export enum DistributedMessageType {
    STATE_REQUEST = 'STATE_REQUEST',
    STATE_RESPONSE = 'STATE_RESPONSE',
    USER_JOINED = 'USER_JOINED',
    USER_LEFT = 'USER_LEFT',
    USER_MESSAGE = 'USER_MESSAGE',
    PRESENCE_UPDATE = 'PRESENCE_UPDATE',
    PRESENCE_REMOVED = 'PRESENCE_REMOVED',
    ASSIGNS_UPDATE = 'ASSIGNS_UPDATE',
    ASSIGNS_REMOVED = 'ASSIGNS_REMOVED',
    EVICT_USER = 'EVICT_USER'
}
