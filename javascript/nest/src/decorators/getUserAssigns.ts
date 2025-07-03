import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetUserAssigns = createParamDecorator(
    (data: void, context) => {
        const userAssigns = context.assigns;

        if (!userAssigns) {
            throw new Error('Invalid decorator usage: GetUserPresences');
        }

        return userAssigns;
    },
);
