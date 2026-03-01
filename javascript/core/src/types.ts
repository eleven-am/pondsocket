import { Server as HTTPServer } from 'http';

import {
    ChannelReceivers,
    PondAssigns,
    PondMessage,
    PondPresence,
    Unsubscribe,
} from '@eleven-am/pondsocket-common';
import { WebSocketServer } from 'ws';

export interface RedisDistributedBackendOptions {
    host?: string;
    port?: number;
    password?: string;
    database?: number;
    url?: string;
    keyPrefix?: string;
    heartbeatIntervalMs?: number;
    heartbeatTimeoutMs?: number;
    onError?: (error: Error) => void;
}

export interface PondSocketOptions {
    server?: HTTPServer;
    socketServer?: WebSocketServer;
    exclusiveServer?: boolean;
    distributedBackend?: IDistributedBackend;
    maxMessageSize?: number;
    heartbeatInterval?: number;
}

export enum DistributedMessageType {
    STATE_REQUEST = 'STATE_REQUEST',
    STATE_RESPONSE = 'STATE_RESPONSE',
    USER_JOINED = 'USER_JOINED',
    USER_LEFT = 'USER_LEFT',
    USER_MESSAGE = 'USER_MESSAGE',
    PRESENCE_UPDATE = 'PRESENCE_UPDATE',
    PRESENCE_REMOVED = 'PRESENCE_REMOVED',
    ASSIGNS_UPDATE = 'ASSIGNS_UPDATE',
    EVICT_USER = 'EVICT_USER',
    NODE_HEARTBEAT = 'NODE_HEARTBEAT'
}

export interface DistributedMessage {
    type: DistributedMessageType;
    endpointName: string;
    channelName: string;
    timestamp?: number;
    sourceNodeId?: string;
}

export interface StateRequest extends DistributedMessage {
    type: DistributedMessageType.STATE_REQUEST;
    fromNode: string;
}

export interface StateResponse extends DistributedMessage {
    type: DistributedMessageType.STATE_RESPONSE;
    users: Array<{
        id: string;
        presence: PondPresence;
        assigns: PondAssigns;
    }>;
}

export interface UserJoined extends DistributedMessage {
    type: DistributedMessageType.USER_JOINED;
    userId: string;
    presence: PondPresence;
    assigns: PondAssigns;
}

export interface UserLeft extends DistributedMessage {
    type: DistributedMessageType.USER_LEFT;
    userId: string;
}

export interface UserMessage extends DistributedMessage {
    type: DistributedMessageType.USER_MESSAGE;
    fromUserId: string;
    event: string;
    payload: PondMessage;
    requestId: string;
    recipientDescriptor: ChannelReceivers;
}

export interface PresenceUpdate extends DistributedMessage {
    type: DistributedMessageType.PRESENCE_UPDATE;
    userId: string;
    presence: PondPresence;
}

export interface PresenceRemoved extends DistributedMessage {
    type: DistributedMessageType.PRESENCE_REMOVED;
    userId: string;
}

export interface AssignsUpdate extends DistributedMessage {
    type: DistributedMessageType.ASSIGNS_UPDATE;
    userId: string;
    assigns: PondAssigns;
}

export interface EvictUser extends DistributedMessage {
    type: DistributedMessageType.EVICT_USER;
    userId: string;
    reason: string;
    fromNode?: string;
}

export interface NodeHeartbeat extends DistributedMessage {
    type: DistributedMessageType.NODE_HEARTBEAT;
    nodeId: string;
}

export type DistributedChannelMessage =
   | StateRequest
   | StateResponse
   | UserJoined
   | UserLeft
   | UserMessage
   | PresenceUpdate
   | PresenceRemoved
   | AssignsUpdate
   | EvictUser
   | NodeHeartbeat;

export interface IDistributedBackend {
    readonly nodeId: string;
    readonly heartbeatTimeoutMs: number;
    initialize(): Promise<void>;
    broadcast(endpointName: string, channelName: string, message: DistributedChannelMessage): Promise<void>;
    subscribeToChannel(endpointName: string, channelName: string, handler: (message: DistributedChannelMessage) => void): Promise<Unsubscribe>;
    subscribeToHeartbeats(handler: (nodeId: string) => void): Unsubscribe;
    cleanup(): Promise<void>;
}
