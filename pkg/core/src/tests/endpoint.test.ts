import { PondPath } from '@eleven-am/pondsocket-common';

import { AuthorizationHandler } from '../abstracts/types';
import { Endpoint } from '../wrappers/endpoint';
import { MockEndpointEngine } from './mocks/endpointEngine';
import { MockWebSocket } from './mocks/mockWebSocket';

describe('Endpoint', () => {
    let endpoint: Endpoint;
    let mockEndpointEngine: MockEndpointEngine;

    beforeEach(() => {
        mockEndpointEngine = new MockEndpointEngine();
        endpoint = new Endpoint(mockEndpointEngine);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    describe('createChannel', () => {
        it('should call createChannel on the endpoint engine', () => {
            const path: PondPath<string> = '/test-channel';
            const handler: AuthorizationHandler<string> = jest.fn();

            endpoint.createChannel(path, handler);

            expect(mockEndpointEngine.createChannel).toHaveBeenCalledWith(path, handler);
        });

        it('should return the result from the endpoint engine', () => {
            const path: PondPath<string> = '/test-channel';
            const handler: AuthorizationHandler<string> = jest.fn();
            const mockResult = { someProperty: 'someValue' };

            mockEndpointEngine.createChannel.mockReturnValue(mockResult as any);

            const result = endpoint.createChannel(path, handler);

            expect(result).toEqual(mockResult);
        });
    });

    describe('closeConnection', () => {
        it('should call closeConnection on the endpoint engine with a single client ID', () => {
            const clientId = 'client1';

            endpoint.closeConnection(clientId);

            expect(mockEndpointEngine.closeConnection).toHaveBeenCalledWith(clientId);
        });

        it('should call closeConnection on the endpoint engine with multiple client IDs', () => {
            const clientIds = ['client1', 'client2', 'client3'];

            endpoint.closeConnection(clientIds);

            expect(mockEndpointEngine.closeConnection).toHaveBeenCalledWith(clientIds);
        });
    });

    describe('getClients', () => {
        it('should call getClients on the endpoint engine and return WebSocket instances', () => {
            const mockClients = [
                { socket: new MockWebSocket() },
                { socket: new MockWebSocket() },
            ];

            mockEndpointEngine.getClients.mockReturnValue(mockClients as any);

            const result = endpoint.getClients();

            expect(mockEndpointEngine.getClients).toHaveBeenCalled();
            expect(result).toHaveLength(2);
            expect(result[0]).toBeInstanceOf(MockWebSocket);
            expect(result[1]).toBeInstanceOf(MockWebSocket);
        });

        it('should return an empty array if no clients are connected', () => {
            mockEndpointEngine.getClients.mockReturnValue([]);

            const result = endpoint.getClients();

            expect(mockEndpointEngine.getClients).toHaveBeenCalled();
            expect(result).toEqual([]);
        });
    });
});
