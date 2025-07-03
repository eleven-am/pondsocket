import { PondPath, ServerActions, UserData } from '@eleven-am/pondsocket-common';

import { BroadcastEvent, LeaveEvent } from '../abstracts/types';
import { EventContext } from '../contexts/eventContext';
import { ChannelEngine } from '../engines/channelEngine';
import { LobbyEngine } from '../engines/lobbyEngine';
import { HttpError } from '../errors/httpError';
import { Channel } from '../wrappers/channel';
import { MockChannelEngine } from './mocks/channelEnegine';
import { MockEndpointEngine } from './mocks/endpointEngine';

describe('LobbyEngine', () => {
    let lobbyEngine: LobbyEngine;
    let mockEndpointEngine: MockEndpointEngine;

    beforeEach(() => {
        mockEndpointEngine = new MockEndpointEngine();
        lobbyEngine = new LobbyEngine(mockEndpointEngine, null);
    });

    describe('onLeave', () => {
        it('should set the leave callback', () => {
            const callback = jest.fn();

            lobbyEngine.onLeave(callback);
            expect(lobbyEngine.leaveCallback).toBe(callback);
        });
    });

    describe('onEvent', () => {
        it('should add an event handler to the middleware', () => {
            const eventPath: PondPath<'test'> = 'test';
            const handler = jest.fn();
            const useSpy = jest.spyOn(lobbyEngine.middleware, 'use');

            lobbyEngine.onEvent(eventPath, handler);

            expect(useSpy).toHaveBeenCalledTimes(1);
            expect(useSpy.mock.calls[0][0]).toBeInstanceOf(Function);
        });
    });

    describe('getOrCreateChannel', () => {
        it('should return the same channel instance for repeated calls with the same name', async () => {
            const channelName = 'testChannel';

            const channel1 = await lobbyEngine.getOrCreateChannel(channelName);
            const channel2 = await lobbyEngine.getOrCreateChannel(channelName);

            expect(channel1).toBeInstanceOf(ChannelEngine);
            expect(channel2).toBeInstanceOf(ChannelEngine);
            expect(channel1).toBe(channel2);
        });

        it('should create different channel instances for different names', async () => {
            const channel1 = await lobbyEngine.getOrCreateChannel('channel1');
            const channel2 = await lobbyEngine.getOrCreateChannel('channel2');

            expect(channel1).toBeInstanceOf(ChannelEngine);
            expect(channel2).toBeInstanceOf(ChannelEngine);
            expect(channel1).not.toBe(channel2);
        });
    });

    describe('getChannel', () => {
        it('should return a channel that has been created', async () => {
            const channelName = 'existingChannel';

            await lobbyEngine.getOrCreateChannel(channelName);

            const result = lobbyEngine.getChannel(channelName);

            expect(result).toBeInstanceOf(ChannelEngine);
            expect(result.name).toBe(channelName);
        });

        it('should throw an error if the channel does not exist', () => {
            const channelName = 'nonExistentChannel';

            expect(() => lobbyEngine.getChannel(channelName)).toThrow(HttpError);
            expect(() => lobbyEngine.getChannel(channelName)).toThrow(`Channel ${channelName} not found`);
        });
    });

    describe('event handling', () => {
        it('should handle events correctly', () => {
            const eventPath: PondPath<'test'> = 'test';
            const handler = jest.fn();

            lobbyEngine.onEvent(eventPath, handler);

            const mockEvent: BroadcastEvent = {
                event: 'test',
                sender: 'user1',
                channelName: 'testChannel',
                requestId: '123',
                payload: { message: 'Hello' },
                action: ServerActions.BROADCAST,
            };

            const mockChannel = new MockChannelEngine();
            const mockNext = jest.fn();

            lobbyEngine.middleware.run(mockEvent, mockChannel, mockNext);

            expect(handler).toHaveBeenCalledTimes(1);
            expect(handler.mock.calls[0][0]).toBeInstanceOf(EventContext);
            expect(handler.mock.calls[0][1]).toBeDefined();
        });
    });

    describe('leave callback', () => {
        it('should trigger the leave callback when a user leaves', () => {
            const leaveCallback = jest.fn();

            lobbyEngine.onLeave(leaveCallback);

            const mockUserData: UserData = {
                id: 'user1',
                assigns: { role: 'member' },
                presence: { status: 'online' },
            };

            const mockChannel = new MockChannelEngine();
            const mockChannelWrapper = new Channel(mockChannel);

            const leaveEvent: LeaveEvent = {
                user: mockUserData,
                channel: mockChannelWrapper,
            };

            lobbyEngine.leaveCallback!(leaveEvent);

            expect(leaveCallback).toHaveBeenCalledTimes(1);
            expect(leaveCallback).toHaveBeenCalledWith(leaveEvent);
        });
    });
});
