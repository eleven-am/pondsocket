import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetEventParams = createParamDecorator(
    (data: void, context) => {
        const event = context.event;

        if (!event) {
            throw new Error('Invalid decorator usage: GetEventParams');
        }

        return event.params;
    },
);
