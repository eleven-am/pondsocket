import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetEventPayload = createParamDecorator(
    (data: void, context) => {
        const event = context.event;

        if (!event) {
            throw new Error('Invalid decorator usage: GetEventPayload');
        }

        return event.payload;
    },
);
