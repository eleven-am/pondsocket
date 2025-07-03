import 'reflect-metadata';
import type { ArgumentMetadata, PipeTransform } from '@nestjs/common';
import type { ModuleRef } from '@nestjs/core';

import { Context } from '../context/context';
import { manageParameters } from '../managers/parametres';
import { managePipes } from '../managers/pipes';
import { ParamDecoratorCallback, Constructor } from '../types';
import { retrieveInstance } from './misc';

function wrapTransform <Input> (metadata: unknown, data: Input, handler: ParamDecoratorCallback<Input>) {
    return async (context: Context, globalPipes: Constructor<PipeTransform>[], moduleRef: ModuleRef) => {
        const value = await handler(data, context, metadata);

        const classTransforms = managePipes(context.getClass()).get();
        const methodTransforms = managePipes(context.getInstance(), context.getMethod()).get();

        const transformers = globalPipes
            .concat(classTransforms, methodTransforms)
            .map((Transformer) => retrieveInstance(moduleRef, Transformer));

        const argumentMetadata: ArgumentMetadata = {
            metatype: metadata as Constructor<unknown>,
            type: 'body',
        };

        let transformedValue = value;

        for (const transformer of transformers) {
            transformedValue = await transformer.transform(transformedValue, argumentMetadata);
        }

        return transformedValue;
    };
}

export function createParamDecorator<Input> (callback: ParamDecoratorCallback<Input>) {
    return (data: Input): ParameterDecorator => (target, propertyKey, index) => {
        const { set } = manageParameters(target, propertyKey as string);
        const type = Reflect.getMetadata('design:paramtypes', target, propertyKey!)[index];

        set(index, wrapTransform(type, data, callback));
    };
}
