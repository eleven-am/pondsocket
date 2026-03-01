import { createServer, Server } from 'http';

import { PondSocket } from '@eleven-am/pondsocket';
import type { Endpoint, PondPath, PondSocketOptions } from '@eleven-am/pondsocket/types';
import type { Express } from 'express';

type EndpointHandler<Path extends string> = Parameters<PondSocket['createEndpoint']>[1];

export interface PondSocketExpress {
    app: Express;
    server: Server;
    pond: PondSocket;
    createEndpoint<Path extends string>(path: PondPath<Path>, handler: EndpointHandler<Path>): Endpoint;
    listen(...args: any[]): Server;
    close(callback?: (err?: Error) => void, timeout?: number): Server;
}

const createPondSocket = (app: Express, options: Omit<PondSocketOptions, 'server'> = {}): PondSocketExpress => {
    const server = createServer(app);
    const pond = new PondSocket({
        ...options,
        server,
    });

    return {
        app,
        server,
        pond,
        createEndpoint: (path, handler) => pond.createEndpoint(path, handler),
        listen: (...args: any[]) => pond.listen(...args),
        close: (callback?, timeout?) => (pond.close as any)(callback, timeout),
    };
};

export default createPondSocket;
