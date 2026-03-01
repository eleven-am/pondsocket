import { createContextParamDecorator } from '../helpers/createContextParamDecorator';

export const GetEventPayload = createContextParamDecorator('event', 'GetEventPayload', (event) => event.payload);
