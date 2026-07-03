import { AnyPondSchema, PondPath } from '@eleven-am/pondsocket-common';

import { AuthorizationHandler } from '../abstracts/types';
import { EndpointEngine } from '../engines/endpointEngine';

export class Endpoint {
    readonly #engine: EndpointEngine;

    constructor (engine: EndpointEngine) {
        this.#engine = engine;
    }

    createChannel<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> (path: PondPath<Path>, handler: AuthorizationHandler<Path, Schema>) {
        return this.#engine.createChannel<Path, Schema>(path, handler);
    }

    closeConnection (clientIds: string | string[]) {
        this.#engine.closeConnection(clientIds);
    }

    getClients () {
        return this.#engine.getClients().map(({ socket }) => socket);
    }
}
