import { createContextParamDecorator } from '../helpers/createContextParamDecorator';

export const GetConnectionHeaders = createContextParamDecorator('connectionContext', 'GetConnectionHeaders', (connection) => connection.headers);
