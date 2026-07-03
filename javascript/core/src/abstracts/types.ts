import { IncomingHttpHeaders } from 'http';
import { IncomingMessage } from 'node:http';
import internal from 'node:stream';

import {
    ChannelEvent,
    AnyPondSchema,
    AssignsOf,
    EventParams,
    EventsOf,
    JoinParamsOf,
    PondAssigns,
    PondEventMap,
    PondMessage,
    PondPresence,
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

export type EventPayload<EventMap extends PondEventMap, Event extends keyof EventMap> =
    EventMap[Event] extends [PondMessage, PondMessage] ? EventMap[Event][0] : EventMap[Event];

export type EventResponse<EventMap extends PondEventMap, Event extends keyof EventMap> =
    EventMap[Event] extends [PondMessage, PondMessage] ? EventMap[Event][1] : PondMessage;

export type EventKey<Schema extends AnyPondSchema> = Extract<keyof EventsOf<Schema>, string>;

export type ConnectionHandler<Path extends string> = (ctx: ConnectionContext<Path>, next: NextFunction) => unknown | Promise<unknown>;

export type AuthorizationHandler<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> = (ctx: JoinContext<Path, Schema>, next: NextFunction) => unknown | Promise<unknown>;

export type EventHandler<Path extends string, Schema extends AnyPondSchema = AnyPondSchema, Event extends EventKey<Schema> = EventKey<Schema>> = (ctx: EventContext<Path, Schema, Event>, next: NextFunction) => unknown | Promise<unknown>;

export type OutgoingEventHandler<Path extends string, Schema extends AnyPondSchema = AnyPondSchema, Event extends EventKey<Schema> = EventKey<Schema>> = (event: OutgoingContext<Path, Schema, Event>, next: NextFunction) => EventPayload<EventsOf<Schema>, Event> | Promise<EventPayload<EventsOf<Schema>, Event>> | void | Promise<void>;

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
    channelSubscriptions?: Map<string, Unsubscribe>;
}

export interface ConnectionResponseOptions {
    engine: EndpointEngine;
    params: ConnectionParams;
    webSocketServer: WebSocketServer;
}

export interface JoinRequestOptions<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> {
    clientId: string;
    assigns: AssignsOf<Schema>;
    joinParams: JoinParamsOf<Schema>;
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
    user: UserData<PondPresence, PondAssigns>;
    channel: Channel;
}

export type LeaveCallback = (event: LeaveEvent) => void;
