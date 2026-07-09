import { createServer, Server } from 'http';

import { PondSocket } from '@eleven-am/pondsocket';
import type { PondSocketOptions } from '@eleven-am/pondsocket/types';
import type { Express } from 'express-serve-static-core';

export interface PondSocketExpress {
    app: Express;
    server: Server;
    pond: PondSocket;
    createEndpoint: PondSocket['createEndpoint'];
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
        createEndpoint: pond.createEndpoint.bind(pond) as PondSocket['createEndpoint'],
        listen: (...args: any[]) => pond.listen(...args),
        close: (callback?, timeout?) => (pond.close as any)(callback, timeout),
    };
};

export default createPondSocket;
