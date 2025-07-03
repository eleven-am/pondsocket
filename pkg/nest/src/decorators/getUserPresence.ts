import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetUserPresence = createParamDecorator(
    (data: void, context) => {
        const userPresences = context.presence;

        if (!userPresences) {
            throw new Error('Invalid decorator usage: GetUserPresences');
        }

        return userPresences;
    },
);
