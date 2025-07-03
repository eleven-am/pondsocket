import { createServer } from 'http';

import PondSocket from '@eleven-am/pondsocket';
import { ConnectionContext, Endpoint, PondPath, PondSocketOptions, RequestHandler } from '@eleven-am/pondsocket/types';
import type { Express } from 'express';

declare global {
    // eslint-disable-next-line @typescript-eslint/no-namespace
    namespace Express {
        export interface Application {
            createEndpoint<Path extends string>(path: PondPath<Path>, handler: RequestHandler<ConnectionContext<Path>>): Endpoint;
        }
    }
}

interface PondSocketExpressApp extends Express {

    /**
     * @desc Accepts a new socket upgrade request on the provided endpoint using the handler function to authenticate the socket
     * @param path - the pattern to accept || can also be a regex
     * @param handler - the handler function to authenticate the socket
     * @example
     * const endpoint = pond.createEndpoint('/api/socket', (req, res) => {
     *    const token = req.query.token;
     *    if (!token)
     *       return res.reject("No token provided");
     *    res.accept({
     *       assign: {
     *           token
     *       }
     *    });
     * })
     */
    createEndpoint<Path extends string>(path: PondPath<Path>, handler: RequestHandler<ConnectionContext<Path>>): Endpoint;
}

/**
 * @desc Creates a pond socket server
 * @param app - The Express app to be used by the server
 * @param options - The options to be used by the pond socket server
 * @constructor
 */
const pondSocket = (app: Express, options: Omit<PondSocketOptions, 'server'>): PondSocketExpressApp => {
    const server = createServer(app);
    const pondSocket = new PondSocket({
        ...options,
        server,
    });

    app.createEndpoint = (path, handler) => pondSocket.createEndpoint(path, handler);
    app.listen = (...args: any[]) => pondSocket.listen(...args);

    return app as PondSocketExpressApp;
};

export default pondSocket;
