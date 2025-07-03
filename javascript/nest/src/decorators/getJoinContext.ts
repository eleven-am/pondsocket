import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetJoinContext = createParamDecorator(
    (data: void, context) => {
        const joinRequest = context.joinContext;

        if (!joinRequest) {
            throw new Error('Invalid decorator usage: GetJoinContext');
        }

        return joinRequest;
    },
);
