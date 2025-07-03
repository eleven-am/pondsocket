import type { PipeTransform } from '@nestjs/common';

import { pondValidatorsKey } from '../constants';
import { Constructor } from '../types';
import { manageClass } from './class';
import { manageMethod } from './method';

const localValidators: Set<Constructor<PipeTransform>> = new Set();

export function managePipes (target: Object, propertyKey?: string) {
    let getter: () => Constructor<PipeTransform>[] | null;
    let setter: (value: Constructor<PipeTransform>[]) => void;

    if (propertyKey) {
        const { get, set } = manageMethod<Constructor<PipeTransform>[]>(pondValidatorsKey, target, propertyKey);

        getter = get;
        setter = set;
    } else {
        const { get, set } = manageClass<Constructor<PipeTransform>[]>(pondValidatorsKey, target);

        getter = get;
        setter = set;
    }

    return {
        get: () => getter() ?? [],
        set: (value: Constructor<PipeTransform>[]) => {
            const current = getter() ?? [];

            setter([...value, ...current]);

            value.forEach((validator) => localValidators.add(validator));
        },
    };
}

export function getLocalPipes () {
    return Array.from(localValidators);
}
