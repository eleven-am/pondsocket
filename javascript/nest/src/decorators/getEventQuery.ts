import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetEventQuery = createParamDecorator(
    (data: void, context) => {
        const event = context.event;

        if (!event) {
            throw new Error('Invalid decorator usage: GetEventQuery');
        }

        return event.query;
    },
);
