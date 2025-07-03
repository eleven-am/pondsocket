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
    return value !== null &&
        value !== undefined &&
        value !== '' &&
        Object.keys(value).length !== 0;
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

    if (context) {
        if (isConnectionContext(context) || isJoinContext(context)) {
            context
                .assign(typeof assigns === 'object' ? assigns : {})
                .accept();
        } else {
            (context as EventContext<string>)
                .assign(typeof assigns === 'object' ? assigns : {});
        }

        if (isNotEmpty(rest)) {
            if (event) {
                context.reply(event, rest);
            }

            if (isJoinContext(context) || isEventContext(context)) {
                if (broadcast) {
                    context.broadcast(broadcast, rest);
                }

                if (broadcastFrom) {
                    context.broadcastFrom(broadcastFrom, rest);
                }

                if (broadcastTo) {
                    context.broadcastTo(broadcastTo.event, rest, broadcastTo.users);
                }
            }
        }
    }

    if (channel) {
        if (isNotEmpty(rest) && !context) {
            if (broadcast || event) {
                const newEvent = (broadcast || event) as string;

                channel.broadcast(newEvent, rest);
            }

            if (broadcastTo) {
                channel.broadcastTo(broadcastTo.users, broadcastTo.event, rest);
            }
        }

        if (isNotEmpty(presence)) {
            channel.upsertPresence(socketId, presence);
        }
    }
}
