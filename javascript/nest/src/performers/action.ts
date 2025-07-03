import type { LeaveEvent, JoinContext, EventContext, ConnectionContext } from '@eleven-am/pondsocket/types';
import type { PipeTransform } from '@nestjs/common';
import type { ModuleRef } from '@nestjs/core';

import { Context } from '../context/context';
import { retrieveInstance } from '../helpers/misc';
import { manageGuards } from '../managers/guards';
import { manageParameters } from '../managers/parametres';
import { NestContext, PondResponse, Constructor, CanActivate } from '../types';
import { isJoinContext, isEventContext, isConnectionContext } from './narrow';
import { performResponse } from './response';

async function retrieveParameters (context: Context, globalPipes: Constructor<PipeTransform>[], moduleRef: ModuleRef) {
    const gottenValues = manageParameters(context.getInstance(), context.getMethod()).get() ?? [];

    const promises = gottenValues
        .map(async ({ callback, index }) => ({
            value: await callback(context, globalPipes, moduleRef),
            index,
        }));

    const values = await Promise.all(promises);

    return values
        .sort((a, b) => a.index - b.index)
        .map(({ value }) => value);
}

async function performGuards (moduleRef: ModuleRef, globalGuards: Constructor<CanActivate>[], context: Context) {
    const classGuards = manageGuards(context.getClass())
        .get();
    const methodGuards = manageGuards(context.getInstance(), context.getMethod())
        .get();

    const guards = globalGuards
        .concat(classGuards, methodGuards)
        .map((Guard) => retrieveInstance(moduleRef, Guard));

    const promises = guards.map((guard) => guard.canActivate(context));
    const results = await Promise.all(promises);

    return results.every((result) => result);
}

function getNestContext (
    context: LeaveEvent | JoinContext<string> | EventContext<string> | ConnectionContext<string>,
): NestContext {
    if (isJoinContext(context)) {
        return {
            join: context,
        }
    }

    if (isEventContext(context)) {
        return {
            event: context,
        }
    }

    if (isConnectionContext(context)) {
        return {
            connection: context,
        }
    }

    return {
        leave: context,
    }
}

export async function performAction (
    instance: any,
    moduleRef: ModuleRef,
    globalGuards: Constructor<CanActivate>[],
    globalPipes: Constructor<PipeTransform>[],
    originalMethod: (...args: any[]) => Promise<PondResponse | null | undefined>,
    propertyKey: string,
    input: LeaveEvent | JoinContext<string> | EventContext<string> | ConnectionContext<string>,
) {
    const ctx = getNestContext(input);
    const context = new Context(ctx, instance, propertyKey);
    const channel = context.channel;
    const socketId = context.user.id;

    const canProceed = await performGuards(moduleRef, globalGuards, context);

    if (canProceed) {
        const data = await originalMethod.apply(
            instance,
            await retrieveParameters(context, globalPipes, moduleRef),
        );

        performResponse(socketId, channel, data, input);
    } else if (isJoinContext(input) || isJoinContext(input)) {
        input.decline('Unauthorized', 403);
    }
}
