import 'reflect-metadata';

export function manageClass<A> (key: symbol, target: any) {
    return {
        get () {
            return (Reflect.getMetadata(key, target) ?? null) as A | null;
        },
        set (value: A) {
            Reflect.defineMetadata(key, value, target);
        },
    };
}
