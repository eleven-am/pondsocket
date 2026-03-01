import { manageClass } from './class';
import { manageMethod } from './method';
import { Constructor } from '../types';

export function createMetadataManager<T> (metadataKey: symbol) {
    const localItems: Set<Constructor<T>> = new Set();

    function manage (target: object, propertyKey?: string) {
        let getter: () => Constructor<T>[] | null;
        let setter: (value: Constructor<T>[]) => void;

        if (propertyKey) {
            const { get, set } = manageMethod<Constructor<T>[]>(metadataKey, target, propertyKey);

            getter = get;
            setter = set;
        } else {
            const { get, set } = manageClass<Constructor<T>[]>(metadataKey, target);

            getter = get;
            setter = set;
        }

        return {
            get: () => getter() ?? [],
            set: (value: Constructor<T>[]) => {
                const current = getter() ?? [];

                setter([...value, ...current]);

                value.forEach((item) => localItems.add(item));
            },
        };
    }

    function getLocalItems () {
        return Array.from(localItems);
    }

    return { manage,
        getLocalItems };
}
