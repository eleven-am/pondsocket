import {
    SystemSender,
    ChannelReceiver,
    ServerActions,
    PondMessage,
    PondAssigns,
    PondPresence,
} from '@eleven-am/pondsocket-common';

import { Channel } from '../wrappers/channel';
import { MockChannelEngine } from './mocks/channelEnegine';

describe('Channel', () => {
    let channel: Channel;
    let mockChannelEngine: MockChannelEngine;

    beforeEach(() => {
        mockChannelEngine = new MockChannelEngine();
        channel = new Channel(mockChannelEngine);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    describe('getUserData', () => {
        it('should call getUser on the channel engine', () => {
            const userId = 'user1';

            channel.getUserData(userId);
            expect(mockChannelEngine.getUserData).toHaveBeenCalledWith(userId);
        });
    });

    describe('broadcast', () => {
        it('should call sendMessage on the channel engine with ALL_USERS', () => {
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello, everyone!' };

            channel.broadcast(event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(
                SystemSender.CHANNEL,
                ChannelReceiver.ALL_USERS,
                ServerActions.BROADCAST,
                event,
                payload,
            );
        });
    });

    describe('broadcastFrom', () => {
        it('should call sendMessage on the channel engine with ALL_EXCEPT_SENDER', () => {
            const userId = 'user1';
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello from user1!' };

            channel.broadcastFrom(userId, event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(
                userId,
                ChannelReceiver.ALL_EXCEPT_SENDER,
                ServerActions.BROADCAST,
                event,
                payload,
            );
        });
    });

    describe('broadcastTo', () => {
        it('should call sendMessage on the channel engine with specific user IDs', () => {
            const userIds = ['user1', 'user2'];
            const event = 'test-event';
            const payload: PondMessage = { message: 'Hello, specific users!' };

            channel.broadcastTo(userIds, event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(
                SystemSender.CHANNEL,
                userIds,
                ServerActions.BROADCAST,
                event,
                payload,
            );
        });
    });

    describe('evictUser', () => {
        it('should call kickUser on the channel engine', () => {
            const userId = 'user1';
            const reason = 'Violated rules';

            channel.evictUser(userId, reason);
            expect(mockChannelEngine.kickUser).toHaveBeenCalledWith(userId, reason);
        });

        it('should use default reason if not provided', () => {
            const userId = 'user1';

            channel.evictUser(userId);
            expect(mockChannelEngine.kickUser).toHaveBeenCalledWith(userId, 'You have been banned from the channel');
        });
    });

    describe('trackPresence', () => {
        it('should call trackPresence on the channel engine', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            channel.trackPresence(userId, presence);
            expect(mockChannelEngine.trackPresence).toHaveBeenCalledWith(userId, presence);
        });
    });

    describe('updatePresence', () => {
        it('should call updatePresence on the channel engine', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'away' };

            channel.updatePresence(userId, presence);
            expect(mockChannelEngine.updatePresence).toHaveBeenCalledWith(userId, presence);
        });
    });

    describe('removePresence', () => {
        it('should call removePresence on the channel engine', () => {
            const userId = 'user1';

            channel.removePresence(userId);
            expect(mockChannelEngine.removePresence).toHaveBeenCalledWith(userId);
        });
    });

    describe('upsertPresence', () => {
        it('should call upsertPresence on the channel engine', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            channel.upsertPresence(userId, presence);
            expect(mockChannelEngine.upsertPresence).toHaveBeenCalledWith(userId, presence);
        });
    });

    describe('updateAssigns', () => {
        it('should call updateAssigns on the channel engine', () => {
            const userId = 'user1';
            const assigns: PondAssigns = { role: 'admin' };

            channel.updateAssigns(userId, assigns);
            expect(mockChannelEngine.updateAssigns).toHaveBeenCalledWith(userId, assigns);
        });
    });
});
