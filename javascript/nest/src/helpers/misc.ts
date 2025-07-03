import type { ModuleRef } from '@nestjs/core';

import { Constructor } from '../types';

export function retrieveInstance<Interface> (moduleRef: ModuleRef, Guard: Constructor<Interface>): Interface {
    try {
        return moduleRef.get(Guard, { strict: false });
    } catch (e) {
        console.warn(`Unable to resolve instance: ${Guard.name}, creating new instance, WARNING: this will not inject dependencies. To fix this, add the instance to the providers array of the PondSocketModule.`);

        return new Guard();
    }
}
