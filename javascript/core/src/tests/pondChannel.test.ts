import { PondPath, PondMessage, SystemSender, ChannelReceiver, ServerActions } from '@eleven-am/pondsocket-common';

import { EventHandler, LeaveCallback } from '../abstracts/types';
import { Channel } from '../wrappers/channel';
import { PondChannel } from '../wrappers/pondChannel';
import { MockChannelEngine } from './mocks/channelEnegine';
import { MockLobbyEngine } from './mocks/lobbyEngine';

describe('PondChannel', () => {
    let pondChannel: PondChannel;
    let mockLobbyEngine: MockLobbyEngine;
    let mockChannelEngine: MockChannelEngine;

    beforeEach(() => {
        mockLobbyEngine = new MockLobbyEngine();
        mockChannelEngine = new MockChannelEngine();
        pondChannel = new PondChannel(mockLobbyEngine);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    describe('onEvent', () => {
        it('should call onEvent on the lobby engine', () => {
            const event: PondPath<string> = '/test-event';
            const handler: EventHandler<string> = jest.fn();

            pondChannel.onEvent(event, handler);

            expect(mockLobbyEngine.onEvent).toHaveBeenCalledWith(event, handler);
        });

        it('should return the PondChannel instance for chaining', () => {
            const event: PondPath<string> = '/test-event';
            const handler: EventHandler<string> = jest.fn();

            const result = pondChannel.onEvent(event, handler);

            expect(result).toBe(pondChannel);
        });
    });

    describe('onLeave', () => {
        it('should set the leaveCallback on the lobby engine', () => {
            const callback: LeaveCallback = jest.fn();

            pondChannel.onLeave(callback);

            expect(mockLobbyEngine.leaveCallback).toBe(callback);
        });

        it('should return the PondChannel instance for chaining', () => {
            const callback: LeaveCallback = jest.fn();

            const result = pondChannel.onLeave(callback);

            expect(result).toBe(pondChannel);
        });
    });

    describe('getChannel', () => {
        it('should call getChannel on the lobby engine and return a Channel instance', () => {
            const channelName = 'test-channel';
            const mockChannelEngine = new MockChannelEngine();

            mockLobbyEngine.getChannel.mockReturnValue(mockChannelEngine);

            const result = pondChannel.getChannel(channelName);

            expect(mockLobbyEngine.getChannel).toHaveBeenCalledWith(channelName);
            expect(result).toBeInstanceOf(Channel);
        });
    });

    describe('broadcast', () => {
        it('should call broadcast on the channel instance', () => {
            const channelName = 'test-channel';
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello, world!' };

            mockLobbyEngine.getChannel.mockReturnValue(mockChannelEngine);
            pondChannel.broadcast(channelName, event, payload);

            expect(mockLobbyEngine.getChannel).toHaveBeenCalledWith(channelName);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(SystemSender.CHANNEL, ChannelReceiver.ALL_USERS, ServerActions.BROADCAST, event, payload);
        });

        it('should return the PondChannel instance for chaining', () => {
            const channelName = 'test-channel';
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello, world!' };

            mockLobbyEngine.getChannel.mockReturnValue(mockChannelEngine);

            const result = pondChannel.broadcast(channelName, event, payload);

            expect(result).toBe(pondChannel);
        });
    });

    describe('broadcastFrom', () => {
        it('should call broadcastFrom on the channel instance', () => {
            const channelName = 'test-channel';
            const userId = 'user1';
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello from user1!' };

            mockLobbyEngine.getChannel.mockReturnValue(mockChannelEngine);

            pondChannel.broadcastFrom(channelName, userId, event, payload);

            expect(mockLobbyEngine.getChannel).toHaveBeenCalledWith(channelName);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(userId, ChannelReceiver.ALL_EXCEPT_SENDER, ServerActions.BROADCAST, event, payload);
        });

        it('should return the PondChannel instance for chaining', () => {
            const channelName = 'test-channel';
            const userId = 'user1';
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello from user1!' };

            mockLobbyEngine.getChannel.mockReturnValue(mockChannelEngine);

            const result = pondChannel.broadcastFrom(channelName, userId, event, payload);

            expect(result).toBe(pondChannel);
        });
    });

    describe('broadcastTo', () => {
        it('should call broadcastTo on the channel instance', () => {
            const channelName = 'test-channel';
            const userIds = ['user1', 'user2'];
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello, specific users!' };

            mockLobbyEngine.getChannel.mockReturnValue(mockChannelEngine);

            pondChannel.broadcastTo(channelName, userIds, event, payload);

            expect(mockLobbyEngine.getChannel).toHaveBeenCalledWith(channelName);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(SystemSender.CHANNEL, userIds, ServerActions.BROADCAST, event, payload);
        });

        it('should return the PondChannel instance for chaining', () => {
            const channelName = 'test-channel';
            const userIds = ['user1', 'user2'];
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello, specific users!' };

            mockLobbyEngine.getChannel.mockReturnValue(mockChannelEngine);

            const result = pondChannel.broadcastTo(channelName, userIds, event, payload);

            expect(result).toBe(pondChannel);
        });
    });
});
