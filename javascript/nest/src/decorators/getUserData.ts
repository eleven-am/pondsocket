import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetUserData = createParamDecorator(
    (data: void, context) => {
        const userData = context.user;

        if (!userData) {
            throw new Error('Invalid decorator usage: GetUserData');
        }

        return userData;
    },
);
