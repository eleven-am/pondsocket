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
        status = error.getStatus();
        const response = error.getResponse();

        if (status < 500) {
            message = error.message;
            data = typeof response === 'object' && response !== null
                ? (response as { message?: unknown }).message
                : response;
        } else {
            message = 'Internal server error';
        }
    } else if (error instanceof Error) {
        message = 'Internal server error';
        status = 500;
    } else {
        message = 'Internal server error';
        status = 500;
    }

    const logMessage = error instanceof Error ? error.message : String(error);

    logger.error(logMessage, error instanceof Error ? error.stack : undefined);

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
