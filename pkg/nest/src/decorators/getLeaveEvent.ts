import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetLeaveEvent = createParamDecorator(
    (data: void, context) => {
        const leaveEvent = context.leaveEvent;

        if (!leaveEvent) {
            throw new Error('Invalid decorator usage: GetLeaveEvent');
        }

        return leaveEvent;
    },
);
