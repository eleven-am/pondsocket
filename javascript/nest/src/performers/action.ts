import type {
    AnyPondChannelDefinition,
    ConnectionContext,
    EventContext,
    JoinContext,
    LeaveEvent,
    OutgoingContext,
    PondEndpointDefinition,
} from '@eleven-am/pondsocket/types';
import {
    ErrorTypes,
    getPondMetadata,
    pondParameterMetadataKey,
} from '@eleven-am/pondsocket-common';
import type { PondParameterDefinition } from '@eleven-am/pondsocket-common';
import type { PipeTransform } from '@nestjs/common';
import type { ModuleRef } from '@nestjs/core';

import { Context } from '../context/context';
import { retrieveInstance } from '../helpers/misc';
import { manageGuards } from '../managers/guards';
import { manageParameters } from '../managers/parametres';
import { CanActivate, Constructor, NestContext, PondLifecycle, PondResponse } from '../types';
import { performResponse } from './response';

type HandlerInput = LeaveEvent | JoinContext<string> | EventContext<string> | ConnectionContext<string> | OutgoingContext<string>;
type HandlerDefinition = AnyPondChannelDefinition | PondEndpointDefinition;

async function retrieveParameters (
    context: Context,
    globalPipes: Constructor<PipeTransform>[],
    moduleRef: ModuleRef,
    activeDefinition?: HandlerDefinition,
) {
    const managedParameters = manageParameters(context.getInstance(), context.getMethod()).get();
    const boundParameters = (
        getPondMetadata<PondParameterDefinition[]>(context.getInstance(), pondParameterMetadataKey) ?? []
    ).filter(({ propertyKey }) => propertyKey === context.getMethod());

    boundParameters.forEach(({ definition }) => {
        if (definition !== activeDefinition) {
            throw new Error(
                `${context.getClass().name}.${context.getMethod()} uses GetContext() from a different PondSocket definition`,
            );
        }
    });

    const parameters = [
        ...managedParameters,
        ...boundParameters.map(({ index }) => ({
            index,
            callback: () => Promise.resolve(context),
        })),
    ];
    const decoratedIndices = new Set(parameters.map(({ index }) => index));

    if (decoratedIndices.size !== parameters.length) {
        throw new Error(
            `${context.getClass().name}.${context.getMethod()} has multiple PondSocket decorators on the same parameter`,
        );
    }

    const reflectedTypes = Reflect.getMetadata(
        'design:paramtypes',
        context.getInstance(),
        context.getMethod(),
    ) as unknown[] | undefined;
    const highestDecoratedIndex = parameters.reduce((highest, { index }) => Math.max(highest, index), -1);
    const parameterCount = Math.max(
        reflectedTypes?.length ?? 0,
        context.getHandler().length,
        highestDecoratedIndex + 1,
    );

    for (let index = 0; index < parameterCount; index++) {
        if (!decoratedIndices.has(index)) {
            throw new Error(
                `${context.getClass().name}.${context.getMethod()} parameter at index ${index} must use a PondSocket parameter decorator`,
            );
        }
    }

    const values: unknown[] = Array.from({ length: parameterCount });
    const resolved = await Promise.all(parameters.map(async ({ callback, index }) => ({
        index,
        value: await callback(context, globalPipes, moduleRef),
    })));

    resolved.forEach(({ index, value }) => {
        values[index] = value;
    });

    return values;
}

async function performGuards (moduleRef: ModuleRef, globalGuards: Constructor<CanActivate>[], context: Context) {
    const classGuards = manageGuards(context.getClass()).get();
    const methodGuards = manageGuards(context.getInstance(), context.getMethod()).get();
    const guards = await Promise.all(globalGuards
        .concat(classGuards, methodGuards)
        .map((Guard) => retrieveInstance(moduleRef, Guard)));

    for (const guard of guards) {
        if (!await guard.canActivate(context)) {
            return false;
        }
    }

    return true;
}

function getNestContext (context: HandlerInput, lifecycle: PondLifecycle): NestContext {
    switch (lifecycle) {
        case 'connection':
            return { connection: context as ConnectionContext<string> };
        case 'join':
            return { join: context as JoinContext<string> };
        case 'event':
            return { event: context as EventContext<string> };
        case 'outgoing':
            return { outgoing: context as OutgoingContext<string> };
        case 'leave':
            return { leave: context as LeaveEvent };
        default:
            throw new Error(`Unsupported PondSocket lifecycle: ${String(lifecycle)}`);
    }
}

function rejectGuard (input: HandlerInput, lifecycle: PondLifecycle) {
    if (lifecycle === 'join' || lifecycle === 'connection') {
        (input as JoinContext<string> | ConnectionContext<string>).decline('Unauthorized', 403);
    } else if (lifecycle === 'event') {
        (input as EventContext<string>).reply(ErrorTypes.UNAUTHORIZED_BROADCAST, {
            code: ErrorTypes.UNAUTHORIZED_BROADCAST,
            message: 'Unauthorized',
            status: 403,
        });
    } else if (lifecycle === 'outgoing') {
        (input as OutgoingContext<string>).block();
    }
}

export async function performAction (
    instance: any,
    moduleRef: ModuleRef,
    globalGuards: Constructor<CanActivate>[],
    globalPipes: Constructor<PipeTransform>[],
    originalMethod: (...args: any[]) => Promise<PondResponse | null | undefined | void> | PondResponse | null | undefined | void,
    propertyKey: string,
    input: HandlerInput,
    lifecycle: PondLifecycle,
    activeDefinition?: HandlerDefinition,
) {
    const context = new Context(getNestContext(input, lifecycle), instance, propertyKey);
    const canProceed = await performGuards(moduleRef, globalGuards, context);

    if (!canProceed) {
        rejectGuard(input, lifecycle);

        return;
    }

    const data = await originalMethod.apply(
        instance,
        await retrieveParameters(context, globalPipes, moduleRef, activeDefinition),
    );

    if (lifecycle === 'outgoing') {
        return;
    }

    performResponse(
        context.user.id,
        context.channel,
        data as PondResponse | null | undefined,
        input as Exclude<HandlerInput, OutgoingContext<string>>,
        lifecycle as Exclude<PondLifecycle, 'outgoing'>,
    );
}
