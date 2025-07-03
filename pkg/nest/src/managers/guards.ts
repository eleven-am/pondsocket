import { manageClass } from './class';
import { manageMethod } from './method';
import { pondGuardsKey } from '../constants';
import { CanActivate, Constructor } from '../types';

const localGuards: Set<Constructor<CanActivate>> = new Set();

export function manageGuards (target: Object, propertyKey?: string) {
    let getter: () => Constructor<CanActivate>[] | null;
    let setter: (value: Constructor<CanActivate>[]) => void;

    if (propertyKey) {
        const { get, set } = manageMethod<Constructor<CanActivate>[]>(pondGuardsKey, target, propertyKey);

        getter = get;
        setter = set;
    } else {
        const { get, set } = manageClass<Constructor<CanActivate>[]>(pondGuardsKey, target);

        getter = get;
        setter = set;
    }

    return {
        get: () => getter() ?? [],
        set: (value: Constructor<CanActivate>[]) => {
            const current = getter() ?? [];

            setter([...value, ...current]);

            value.forEach((guard) => localGuards.add(guard));
        },
    };
}

export function getLocalGuards () {
    return Array.from(localGuards);
}
