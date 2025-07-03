import { SocketCache } from '../../abstracts/types';
import { EndpointEngine } from '../../engines/endpointEngine';

export class MockEndpointEngine extends EndpointEngine {
    user = {} as SocketCache;

    createChannel = jest.fn();

    closeConnection = jest.fn();

    getClients = jest.fn();

    manageSocket = jest.fn((user) => this.user = user);

    getUser = jest.fn((x) => this.user);

    sendMessage = jest.fn();

    createManager = jest.fn();

    path = 'String';

    constructor () {
        super('', null);
    }
}
