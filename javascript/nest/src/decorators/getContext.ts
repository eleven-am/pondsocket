import { createParamDecorator } from '../helpers/createParamDecorator';

export const GetContext = createParamDecorator(
    (data: void, context) => context,
);
