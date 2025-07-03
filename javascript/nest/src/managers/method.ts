import 'reflect-metadata';

export function manageMethod<A> (key: symbol, target: any, propertyKey: string) {
    return {
        get () {
            return (Reflect.getMetadata(key, target, propertyKey) ?? null) as A | null;
        },
        set (value: A) {
            Reflect.defineMetadata(key, value, target, propertyKey);
        },
    };
}
