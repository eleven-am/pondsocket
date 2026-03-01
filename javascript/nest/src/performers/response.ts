import {
    Channel,
    JoinContext,
    EventContext,
    ConnectionContext, LeaveEvent,
} from '@eleven-am/pondsocket/types';

import { PondResponse } from '../types';
import {
    isJoinContext,
    isEventContext,
    isConnectionContext, isLeaveEvent,
} from './narrow';

function isNotEmpty<TValue> (value: TValue | null | undefined): value is TValue {
    if (value === null || value === undefined) {
        return false;
    }

    if (typeof value === 'object') {
        return Object.keys(value).length !== 0;
    }

    return value !== '';
}

export function performResponse (
    socketId: string,
    channel: Channel | null,
    data: PondResponse | null | undefined,
    context: JoinContext<string> | EventContext<string> | ConnectionContext<string> | LeaveEvent,
) {
    if ((isLeaveEvent(context) || !isNotEmpty(data)) || (!isEventContext(context) && context.hasResponded)) {
        return;
    }

    const {
        event,
        presence,
        assigns,
        broadcast,
        broadcastFrom,
        broadcastTo,
        ...rest
    } = data;

    if (isConnectionContext(context) || isJoinContext(context)) {
        context
            .assign(typeof assigns === 'object' ? assigns : {})
            .accept();
    } else {
        (context as EventContext<string>)
            .assign(typeof assigns === 'object' ? assigns : {});
    }

    const payload = isNotEmpty(rest) ? rest : {};

    if (event) {
        context.reply(event, payload);
    }

    if (isJoinContext(context) || isEventContext(context)) {
        if (broadcast) {
            context.broadcast(broadcast, payload);
        }

        if (broadcastFrom) {
            context.broadcastFrom(broadcastFrom, payload);
        }

        if (broadcastTo) {
            context.broadcastTo(broadcastTo.event, payload, broadcastTo.users);
        }
    }

    if (channel && isNotEmpty(presence)) {
        channel.upsertPresence(socketId, presence);
    }
}
