import type {
    Channel,
    ConnectionContext,
    EventContext,
    JoinContext,
    LeaveEvent,
} from '@eleven-am/pondsocket/types';
import { buildPondRoute } from '@eleven-am/pondsocket-common';

import { PondLifecycle, PondResponse } from '../types';

type ResponseContext = JoinContext<string> | EventContext<string> | ConnectionContext<string> | LeaveEvent;

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
    context: ResponseContext,
    lifecycle: Exclude<PondLifecycle, 'outgoing'>,
) {
    if (lifecycle === 'leave' || !isNotEmpty(data)) {
        return;
    }

    if ((lifecycle === 'join' || lifecycle === 'connection') && (context as JoinContext<string> | ConnectionContext<string>).hasResponded) {
        return;
    }

    const {
        event,
        eventParams,
        presence,
        assigns,
        broadcast,
        broadcastFrom,
        broadcastTo,
        decline,
        payload: explicitPayload,
        ...legacyPayload
    } = data;
    const responseContext = context as JoinContext<string> | EventContext<string> | ConnectionContext<string>;

    if (decline && (lifecycle === 'join' || lifecycle === 'connection')) {
        const options = typeof decline === 'string' ? { message: decline } : decline;

        (responseContext as JoinContext<string> | ConnectionContext<string>).decline(options.message, options.status);

        return;
    }

    if (assigns !== undefined) {
        responseContext.assign(assigns);
    }

    if (lifecycle === 'connection' || lifecycle === 'join') {
        (responseContext as JoinContext<string> | ConnectionContext<string>).accept();
    }

    const payload = explicitPayload ?? (isNotEmpty(legacyPayload) ? legacyPayload : {});

    if (event) {
        responseContext.reply(eventParams ? buildPondRoute(event, eventParams) : event, payload);
    }

    if (lifecycle === 'join' || lifecycle === 'event') {
        const channelContext = responseContext as JoinContext<string> | EventContext<string>;

        if (broadcast) {
            channelContext.broadcast(eventParams ? buildPondRoute(broadcast, eventParams) : broadcast, payload);
        }

        if (broadcastFrom) {
            channelContext.broadcastFrom(eventParams ? buildPondRoute(broadcastFrom, eventParams) : broadcastFrom, payload);
        }

        if (broadcastTo) {
            const broadcastEvent = eventParams ? buildPondRoute(broadcastTo.event, eventParams) : broadcastTo.event;

            channelContext.broadcastTo(broadcastEvent, payload, broadcastTo.users);
        }
    }

    if (channel && presence !== undefined) {
        channel.upsertPresence(socketId, presence);
    }
}
