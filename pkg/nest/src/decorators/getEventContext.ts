import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetEventContext = createParamDecorator(
    (data: void, context) => {
        const joinContext = context.joinContext;

        if (!joinContext) {
            throw new Error('Invalid decorator usage: GetEventContext');
        }

        return joinContext;
    },
);
