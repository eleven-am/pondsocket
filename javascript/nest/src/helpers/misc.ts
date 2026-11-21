import type { ContextId, ModuleRef } from '@nestjs/core' with { 'resolution-mode': 'import' };

import { Constructor } from '../types';

export async function retrieveInstance<Interface> (
    moduleRef: ModuleRef,
    Type: Constructor<Interface>,
    contextId?: ContextId,
): Promise<Interface> {
    try {
        return moduleRef.get(Type, { strict: false });
    } catch {
        try {
            return await moduleRef.resolve(Type, contextId, { strict: false });
        } catch (error) {
            if (error instanceof Error) {
                error.message = `Unable to resolve ${Type.name}. ` +
                    `Register it in the PondSocketModule providers array. ${error.message}`;

                throw error;
            }

            throw error;
        }
    }
}
