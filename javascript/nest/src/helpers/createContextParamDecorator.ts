import { createParamDecorator } from './createParamDecorator';
import { Context } from '../context/context';

export function createContextParamDecorator<K extends keyof Context> (
    key: K,
    decoratorName: string,
    transform?: (value: NonNullable<Context[K]>) => unknown,
) {
    return createParamDecorator(
        (data: void, context: Context) => {
            const value = context[key];

            if (value === null || value === undefined) {
                throw new Error(`Invalid decorator usage: ${decoratorName}`);
            }

            return transform ? transform(value as NonNullable<Context[K]>) : value;
        },
    );
}
