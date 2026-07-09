import { definePondEndpoint } from '@eleven-am/pondsocket';
import express from 'express';

import createPondSocket from './index';

describe('PondSocket Express', () => {
    it('creates an endpoint from a shared definition without exposing pond', async () => {
        const Socket = definePondEndpoint('/v1/socket/:tenantId');
        const server = createPondSocket(express());
        const endpoint = server.createEndpoint(Socket, (context) => {
            context.params.tenantId.toUpperCase();
            context.accept();
        });

        expect(endpoint).toBeDefined();

        await server.pond.closeAsync(25);
    });
});
