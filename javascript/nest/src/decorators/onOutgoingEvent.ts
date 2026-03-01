import { manageOutgoingEvent } from '../managers/outgoingEvent';

export function OnOutgoingEvent (event = '*'): MethodDecorator {
    return (target, propertyKey, descriptor) => {
        const originalMethod = descriptor.value as (...args: any[]) => any;
        const { set } = manageOutgoingEvent(target);

        set(event, async (instance, _moduleRef, _globalGuards, _globalPipes, ctx) => {
            await originalMethod.call(instance, ctx);
        });
    };
}
