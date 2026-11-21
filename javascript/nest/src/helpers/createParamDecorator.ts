import 'reflect-metadata';
import type { ArgumentMetadata, PipeTransform } from '@nestjs/common' with { 'resolution-mode': 'import' };
import type { ContextId, ModuleRef } from '@nestjs/core' with { 'resolution-mode': 'import' };

import { Context } from '../context/context';
import { manageParameters } from '../managers/parametres';
import { managePipes } from '../managers/pipes';
import { ParamDecoratorCallback, Constructor } from '../types';
import { retrieveInstance } from './misc';

function wrapTransform <Input> (metadata: unknown, data: Input, handler: ParamDecoratorCallback<Input>) {
    return async (
        context: Context,
        globalPipes: Constructor<PipeTransform>[],
        moduleRef: ModuleRef,
        contextId?: ContextId,
    ) => {
        const value = await handler(data, context, metadata);

        const classTransforms = managePipes(context.getClass()).get();
        const methodTransforms = managePipes(context.getInstance(), context.getMethod()).get();

        const transformers = await Promise.all(globalPipes.concat(classTransforms, methodTransforms)
            .map((Transformer) => retrieveInstance(moduleRef, Transformer, contextId)));

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
        const parameterTypes = Reflect.getMetadata('design:paramtypes', target, propertyKey!) as unknown[] | undefined;
        const type = parameterTypes?.[index];

        set(index, wrapTransform(type, data, callback));
    };
}
