import { createContextParamDecorator } from '../helpers/createContextParamDecorator';

export const GetEventQuery = createContextParamDecorator('event', 'GetEventQuery', (event) => event.query);
