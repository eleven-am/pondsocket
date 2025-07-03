import type { PipeTransform } from '@nestjs/common';

import { managePipes } from '../managers/pipes';
import { Constructor } from '../types';

export function UsePipes (...validators: Constructor<PipeTransform>[]): ClassDecorator | MethodDecorator {
    return (target, propertyKey) => {
        const { set } = managePipes(target, propertyKey as string | undefined);

        set(validators);
    };
}
