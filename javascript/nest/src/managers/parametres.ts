import 'reflect-metadata';
import type { PipeTransform } from '@nestjs/common' with { 'resolution-mode': 'import' };
import type { ContextId, ModuleRef } from '@nestjs/core' with { 'resolution-mode': 'import' };

import { parametersKey } from '../constants';
import { Context } from '../context/context';
import { ParamDecoratorMetadata, Constructor } from '../types';
import { manageMethod } from './method';

export function manageParameters (target: any, propertyKey: string) {
    const { get, set } = manageMethod<ParamDecoratorMetadata[]>(
        parametersKey,
        target,
        propertyKey,
    );

    return {
        get () {
            return get() || [];
        },
        set (index: number, callback: (
            context: Context,
            globalPipes: Constructor<PipeTransform>[],
            moduleRef: ModuleRef,
            contextId?: ContextId,
        ) => Promise<unknown>) {
            const handlers = get() || [];

            set([
                ...handlers,
                {
                    index,
                    callback,
                },
            ]);
        },
    };
}
