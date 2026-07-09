import type { ModuleRef } from '@nestjs/core';

import { Constructor } from '../types';

export async function retrieveInstance<Interface> (moduleRef: ModuleRef, Type: Constructor<Interface>): Promise<Interface> {
    try {
        return moduleRef.get(Type, { strict: false });
    } catch {
        try {
            return await moduleRef.resolve(Type, undefined, { strict: false });
        } catch {
            throw new Error(`Unable to resolve ${Type.name}. Register it in the PondSocketModule providers array.`);
        }
    }
}
