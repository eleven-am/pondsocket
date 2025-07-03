import 'reflect-metadata';
import type { PipeTransform } from '@nestjs/common';
import type { ModuleRef } from '@nestjs/core';

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
        set (index: number, callback: (context: Context, globalPipes: Constructor<PipeTransform>[], moduleRef: ModuleRef) => Promise<unknown>) {
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
