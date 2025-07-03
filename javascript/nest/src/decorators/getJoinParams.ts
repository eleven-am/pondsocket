import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetJoinParams = createParamDecorator(
    (data: void, context) => {
        const joinRequest = context.joinContext;

        if (!joinRequest) {
            throw new Error('Invalid decorator usage: GetJoinParams');
        }

        return joinRequest.joinParams;
    },
);
