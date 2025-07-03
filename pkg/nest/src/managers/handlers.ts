import { HandlerData, HandlerFunction } from '../types';
import { manageClass } from './class';

export function manageHandlers<Context> (key: symbol, target: any) {
    const { get, set } = manageClass<HandlerData<Context>[]>(
        key,
        target,
    );

    return {
        get () {
            return get() || [];
        },
        set (
            path: string,
            value: HandlerFunction<Context>,
        ) {
            const handlers = get() || [];

            set([
                ...handlers,
                {
                    path,
                    value,
                },
            ]);
        },
    };
}
