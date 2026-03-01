import { createContextParamDecorator } from '../helpers/createContextParamDecorator';

export const GetEventParams = createContextParamDecorator('event', 'GetEventParams', (event) => event.params);
