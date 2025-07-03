import type {
    JoinContext,
    EventContext,
    ConnectionContext,
    LeaveEvent,
} from '@eleven-am/pondsocket/types';

type Context = JoinContext<string> | EventContext<string> | ConnectionContext<string> | LeaveEvent;

export function isEventContext (response: Context): response is EventContext<string> {
    return !('accept' in response);
}

export function isJoinContext (response: Context): response is JoinContext<string> {
    return 'accept' in response && 'broadcast' in response;
}

export function isConnectionContext (response: Context): response is ConnectionContext<string> {
    return 'accept' in response && !('broadcast' in response);
}

export function isLeaveEvent (response: Context): response is LeaveEvent {
    const isJoin = isJoinContext(response);
    const isEvent = isEventContext(response);
    const isConnection = isConnectionContext(response);

    return !isJoin && !isEvent && !isConnection;
}