import type { PondEvent, UserData } from '@eleven-am/pondsocket-common';
import { NestContext } from '../types';

export class Context {
    readonly #data: Record<string, unknown> = {};

    readonly #context: NestContext;

    readonly #instance: any;

    readonly #propertyKey: string;

    constructor (
        context: NestContext,
        instance: any,
        propertyKey: string,
    ) {
        this.#context = context;
        this.#instance = instance;
        this.#propertyKey = propertyKey;
    }

    get joinContext () {
        return this.#context.join ?? null;
    }

    get eventContext () {
        return this.#context.event ?? null;
    }

    get connectionContext () {
        return this.#context.connection ?? null;
    }

    get leaveEvent () {
        return this.#context.leave ?? null;
    }

    get presence () {
        return this.user.presence;
    }

    get assigns () {
        return this.user.assigns;
    }

    get user () {
        if (this.connectionContext) {
            const user: UserData = {
                id: this.connectionContext.clientId,
                assigns: {},
                presence: {},
            };

            return user;
        } else if (this.leaveEvent) {
            return this.leaveEvent.user;
        }

        return this.joinContext?.user ?? this.eventContext!.user;
    }

    get channel () {
        return this.joinContext?.channel ?? this.eventContext?.channel ?? this.leaveEvent?.channel ?? null;
    }

    get event () {
        if (this.connectionContext) {
            const event: PondEvent<string> = {
                params: this.connectionContext.params,
                query: this.connectionContext.query,
                payload: {},
                event: 'CONNECTION',
            };

            return event;
        }

        return this.joinContext?.event ?? this.eventContext?.event ?? null;
    }

    getClass () {
        return this.#instance.constructor;
    }

    getHandler () {
        return this.#instance[this.#propertyKey];
    }

    getInstance () {
        return this.#instance;
    }

    getMethod () {
        return this.#propertyKey;
    }

    addData (key: string, value: unknown) {
        this.#data[key] = value;
    }

    getData (key: string) {
        return this.#data[key] ?? null;
    }
}
