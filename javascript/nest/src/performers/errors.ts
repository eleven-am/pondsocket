import type { ConnectionContext, EventContext, JoinContext } from '@eleven-am/pondsocket/types';
import { ErrorTypes } from '@eleven-am/pondsocket-common';
import { HttpException, Logger } from '@nestjs/common';

import { PondLifecycle } from '../types';

const logger = new Logger('PondSocketHandler');

export function performErrors (
    error: unknown,
    response: ConnectionContext<string> | JoinContext<string> | EventContext<string>,
    lifecycle: 'connection' | 'join' | 'event',
) {
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

    logger.error(message, error instanceof Error ? error.stack : undefined);

    if (lifecycle === 'event') {
        return (response as EventContext<string>).reply(ErrorTypes.INTERNAL_SERVER_ERROR, {
            code: ErrorTypes.INTERNAL_SERVER_ERROR,
            message,
            data,
            status,
        });
    }

    const pendingResponse = response as ConnectionContext<string> | JoinContext<string>;

    if (!pendingResponse.hasResponded) {
        return pendingResponse.decline(message, status);
    }
}

export function logHandlerError (error: unknown, lifecycle: PondLifecycle, propertyKey: string | symbol) {
    const message = error instanceof Error ? error.message : String(error);

    logger.error(`Error in ${lifecycle} handler "${String(propertyKey)}": ${message}`, error instanceof Error ? error.stack : undefined);
}
