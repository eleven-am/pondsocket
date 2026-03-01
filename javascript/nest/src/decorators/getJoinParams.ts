import { createContextParamDecorator } from '../helpers/createContextParamDecorator';

export const GetJoinParams = createContextParamDecorator('joinContext', 'GetJoinParams', (joinRequest) => joinRequest.joinParams);
