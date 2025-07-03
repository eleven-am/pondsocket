import type { ConnectionContext, JoinContext, EventContext } from '@eleven-am/pondsocket/types';
// eslint-disable-next-line import/no-unresolved
import { HttpException } from '@nestjs/common';

import { isEventContext } from './narrow';

export function performErrors (error: unknown, response: ConnectionContext<string> | JoinContext<string> | EventContext<string>) {
    let message: string;
    let data: unknown;
    let status: number;

    if (error instanceof HttpException) {
        message = error.message;
        status = error.getStatus();
        data = (error.getResponse() as any)?.message;
    } else if (error instanceof Error) {
        message = error.message;
        status = 500;
    } else {
        message = 'An unknown error occurred';
        status = 500;
    }

    if (isEventContext(response)) {
        return response.reply('UNKNOWN_ERROR', {
            message,
            data,
            status,
        });
    }

    if (!response.hasResponded) {
        return response.decline(message, status);
    }

    if (process.env.NODE_ENV === 'development') {
        console.error(error);
    }
}
