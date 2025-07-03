export function manageProperty<A> (key: symbol, target: any) {
    function build <T> (propertyKey: string, callback?: (value: A) => A | T | null) {
        Object.defineProperty(target, propertyKey, {
            get () {
                const value = Reflect.getMetadata(key, this) as A;

                if (callback) {
                    return callback(value);
                }

                return value;
            },
            set () {
                throw new Error(`${propertyKey} is readonly`);
            },
            enumerable: true,
            configurable: true,
        });
    }

    function set (value: A) {
        Reflect.defineMetadata(key, value, target);
    }

    return {
        build,
        set,
    };
}
