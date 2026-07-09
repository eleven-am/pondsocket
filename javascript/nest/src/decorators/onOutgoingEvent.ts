import { manageOutgoingEvent } from '../managers/outgoingEvent';
import { performAction } from '../performers/action';
import { logHandlerError } from '../performers/errors';
import type { PondResponse } from '../types';

export function OnOutgoingEvent (event = '*'): MethodDecorator {
    return (target, propertyKey, descriptor) => {
        const originalMethod = descriptor.value as (...args: any[]) =>
            PondResponse | null | undefined | void |
            Promise<PondResponse | null | undefined | void>;
        const { set } = manageOutgoingEvent(target);

        set(event, async (instance, moduleRef, globalGuards, globalPipes, ctx) => {
            try {
                await performAction(
                    instance,
                    moduleRef,
                    globalGuards,
                    globalPipes,
                    originalMethod,
                    propertyKey as string,
                    ctx,
                    'outgoing',
                );
            } catch (error) {
                logHandlerError(error, 'outgoing', propertyKey);
                ctx.block();
            }
        });
    };
}
