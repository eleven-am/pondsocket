import { IncomingHttpHeaders } from 'http';

import { ChannelReceiver, PresenceEventTypes, ServerActions, PubSubEvents } from '../enums';

export type Unsubscribe = () => void;

type IsParam<Path> = Path extends `:${infer Param}` ? Param : never;

type FilteredParams<Path> = Path extends `${infer First}/${infer Second}`
    ? IsParam<First> | FilteredParams<Second>
    : IsParam<Path>

export type Params<Path> = {
    [Key in FilteredParams<Path>]: string
}

export type PondPath<Path extends string> = Path | RegExp;

export type EventParams<Path> = {
    query: Record<string, string>;
    params: Params<Path>;
}

export type PondObject = Record<string, any>;
export type PondPresence = PondObject;
export type PondMessage = PondObject;
export type PondAssigns = PondObject;
export type JoinParams = PondObject;

export interface PresencePayload<Presence extends PondPresence = PondPresence> {
    changed: Presence;
    presence: Presence[];
}

export interface Event {
    action: ServerActions.CONNECT | ServerActions.BROADCAST | ServerActions.SYSTEM | ServerActions.ERROR;
    event: string;
    payload: PondMessage;
    channelName: string;
    requestId: string;
}

interface PresenceEventMessage {
    action: ServerActions.PRESENCE;
    event: PresenceEventTypes;
    channelName: string;
    payload: PresencePayload;
    requestId: string;
}

export type ChannelEvent = Event | PresenceEventMessage;

export type PondEvent<Path> = EventParams<Path> & {
    payload: PondMessage;
    event: string;
}

export type IncomingConnection<Path> = EventParams<Path> & {
    id: string;
    headers: IncomingHttpHeaders;
    address: string;
    cookies: Record<string, string>;
}

interface PubSubGetPresenceCommand {
    endpoint: PondPath<string>;
    event: PubSubEvents.GET_PRESENCE;
    pubSubId: string;
    channel: string;
}

interface PubSubPresenceEvent {
    channel: string;
    pubSubId: string;
    presence: UserPresences;
    event: PubSubEvents.PRESENCE;
    endpoint: PondPath<string>;
}

interface PubSubMessageEvent {
    pubSubId: string;
    channel: string;
    message: ChannelEvent;
    recipient: ChannelReceivers;
    event: PubSubEvents.MESSAGE;
    endpoint: PondPath<string>;
}

export type PubSubEvent = PubSubGetPresenceCommand | PubSubPresenceEvent | PubSubMessageEvent;

export type PondEventMap = Record<string, [PondMessage, PondMessage] | PondMessage>;

export type ChannelReceivers = ChannelReceiver | string[];

export type EventWithResponse<EventMap extends PondEventMap> = {
    [Event in keyof EventMap]: EventMap[Event] extends [PondMessage, PondMessage] ? Event : never;
}[keyof EventMap];

export type PayloadForResponse<EventMap extends PondEventMap, Event extends EventWithResponse<EventMap>> = EventMap[Event] extends [PondMessage, PondMessage] ? EventMap[Event][0] : never;

export type ResponseForEvent<EventMap extends PondEventMap, Event extends EventWithResponse<EventMap>> = EventMap[Event] extends [PondMessage, PondMessage] ? EventMap[Event][1] : never;

export interface UserData<PresenceType extends PondPresence = PondPresence, AssignType extends PondAssigns = PondAssigns> {
    assigns: AssignType;
    presence: PresenceType;
    id: string;
}

export type UserAssigns<AssignType extends PondAssigns = PondAssigns> = Record<string, AssignType>;

export type UserPresences<PresenceType extends PondPresence = PondPresence> = Record<string, PresenceType>;
