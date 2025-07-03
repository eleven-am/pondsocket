import { MockLobbyEngine } from './lobbyEngine';
import { ChannelEngine } from '../../engines/channelEngine';

export class MockChannelEngine extends ChannelEngine {
    addUser = jest.fn();

    sendMessage = jest.fn();

    broadcastMessage = jest.fn();

    trackPresence = jest.fn();

    updatePresence = jest.fn();

    removePresence = jest.fn();

    upsertPresence = jest.fn();

    updateAssigns = jest.fn();

    kickUser = jest.fn();

    getAssigns = jest.fn();

    getPresence = jest.fn();

    destroy = jest.fn();

    removeUser = jest.fn();

    getUserData = jest.fn();

    parent = new MockLobbyEngine();

    constructor () {
        super(new MockLobbyEngine(), 'mockChannel', null);
    }

    private _name = 'mockChannel';

    public get name () {
        return this._name;
    }

    public set name (value) {
        this._name = value;
    }
}
