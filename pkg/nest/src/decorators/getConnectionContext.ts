import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetConnectionContext = createParamDecorator(
    (data: void, context) => {
        const connection = context.connectionContext;

        if (!connection) {
            throw new Error('Invalid decorator usage: GetConnectionContext');
        }

        return connection;
    },
);
