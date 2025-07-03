import { manageLeave } from '../managers/leave';
import { performAction } from '../performers/action';
import { PondResponse } from '../types';

export function OnLeave (): MethodDecorator {
    return (target, propertyKey, descriptor) => {
        const originalMethod = descriptor.value as (...args: any[]) => Promise<PondResponse | null | undefined>;
        const { set } = manageLeave(target);

        set('', async (instance, moduleRef, globalGuards, globalPipes, leaveEvent) => {
            try {
                await performAction(
                    instance,
                    moduleRef,
                    globalGuards,
                    globalPipes,
                    originalMethod,
                    propertyKey as string,
                    leaveEvent,
                );
            } catch (err) {
                // no-op
            }
        });
    };
}
