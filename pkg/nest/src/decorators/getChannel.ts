import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetChannel = createParamDecorator(
    (data: void, context) => {
        const channel = context.channel;

        if (!channel) {
            throw new Error('Invalid decorator usage: GetChannel');
        }

        return channel;
    },
);
