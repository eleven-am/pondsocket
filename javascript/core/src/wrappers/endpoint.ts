import {
    AnyPondChannelDefinition,
    AnyPondSchema,
    PathOf,
    PondPath,
    SchemaOf,
} from '@eleven-am/pondsocket-common';

import { PondChannel } from './pondChannel';
import { AuthorizationHandler } from '../abstracts/types';
import { EndpointEngine } from '../engines/endpointEngine';

export class Endpoint {
    readonly #engine: EndpointEngine;

    constructor (engine: EndpointEngine) {
        this.#engine = engine;
    }

    createChannel<Definition extends AnyPondChannelDefinition> (definition: Definition, handler: AuthorizationHandler<PathOf<Definition>, SchemaOf<Definition>>): PondChannel<SchemaOf<Definition>>;

    createChannel<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> (path: PondPath<Path>, handler: AuthorizationHandler<Path, Schema>): PondChannel<Schema>;

    createChannel<Path extends string, Schema extends AnyPondSchema = AnyPondSchema> (path: PondPath<Path> | AnyPondChannelDefinition, handler: AuthorizationHandler<Path, Schema>) {
        return this.#engine.createChannel(path as PondPath<Path>, handler);
    }

    closeConnection (clientIds: string | string[]) {
        this.#engine.closeConnection(clientIds);
    }

    getClients () {
        return this.#engine.getClients().map(({ socket }) => socket);
    }
}
