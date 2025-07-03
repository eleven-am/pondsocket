import { MockEndpointEngine } from './endpointEngine';
import { Middleware } from '../../abstracts/middleware';
import { BroadcastEvent, LeaveCallback } from '../../abstracts/types';
import { ChannelEngine } from '../../engines/channelEngine';
import { EndpointEngine } from '../../engines/endpointEngine';
import { LobbyEngine } from '../../engines/lobbyEngine';

export class MockLobbyEngine extends LobbyEngine {
    middleware = {
        run: jest.fn(),
        use: jest.fn(),
    } as unknown as Middleware<BroadcastEvent, ChannelEngine>;

    leaveCallback: LeaveCallback | undefined;

    getChannel = jest.fn();

    onEvent = jest.fn();

    parent = new MockEndpointEngine();

    constructor () {
        super({} as EndpointEngine, null);
    }
}
