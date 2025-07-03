import { PondEvent, PondPresence, PondAssigns, PondMessage, ServerActions } from '@eleven-am/pondsocket-common';

import { BroadcastEvent } from '../abstracts/types';
import { EventContext } from '../contexts/eventContext';
import { Channel } from '../wrappers/channel';
import { MockChannelEngine } from './mocks/channelEnegine';

describe('EventContext', () => {
    let mockChannelEngine: MockChannelEngine;
    let eventContext: EventContext<string>;
    const mockEvent: BroadcastEvent = {
        event: 'test-event',
        sender: 'user1',
        channelName: 'test-channel',
        requestId: 'request-123',
        action: ServerActions.BROADCAST,
        payload: { message: 'Hello, world!' },
    };
    const mockParams = {
        params: { id: '123' },
        query: { sort: 'asc' },
    };

    beforeEach(() => {
        mockChannelEngine = new MockChannelEngine();
        mockChannelEngine.name = 'test-channel';
        eventContext = new EventContext(mockEvent, mockParams, mockChannelEngine);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    // EventRequest tests
    describe('event', () => {
        it('should return the correct PondEvent object', () => {
            const expectedEvent: PondEvent<string> = {
                event: 'test-event',
                params: { id: '123' },
                query: { sort: 'asc' },
                payload: { message: 'Hello, world!' },
            };

            expect(eventContext.event).toEqual(expectedEvent);
        });
    });

    describe('channelName', () => {
        it('should return the correct channel name', () => {
            expect(eventContext.channelName).toBe('test-channel');
        });
    });

    describe('channel', () => {
        it('should return a Channel instance', () => {
            const channel = eventContext.channel;

            expect(channel).toBeInstanceOf(Channel);
        });
    });

    describe('presences', () => {
        it('should return the presences from the channel engine', () => {
            const mockPresences = {
                user1: { status: 'online' },
                user2: { status: 'away' },
            };

            mockChannelEngine.getPresence.mockReturnValue(mockPresences);

            expect(eventContext.presences).toEqual(mockPresences);
            expect(mockChannelEngine.getPresence).toHaveBeenCalled();
        });
    });

    describe('assigns', () => {
        it('should return the assigns from the channel engine', () => {
            const mockAssigns = {
                user1: { role: 'admin' },
                user2: { role: 'user' },
            };

            mockChannelEngine.getAssigns.mockReturnValue(mockAssigns);

            expect(eventContext.assigns).toEqual(mockAssigns);
            expect(mockChannelEngine.getAssigns).toHaveBeenCalled();
        });
    });

    describe('user', () => {
        it('should return the user data from the channel engine', () => {
            const mockUserData = {
                id: 'user1',
                assigns: { role: 'admin' } as PondAssigns,
                presence: { status: 'online' } as PondPresence,
            };

            mockChannelEngine.getUserData.mockReturnValue(mockUserData);

            expect(eventContext.user).toEqual(mockUserData);
            expect(mockChannelEngine.getUserData).toHaveBeenCalledWith('user1');
        });
    });

    // EventResponse tests
    describe('assign', () => {
        it('should call updateAssigns on the channel engine', () => {
            const assigns: PondAssigns = { role: 'admin' };

            eventContext.assign(assigns);
            expect(mockChannelEngine.updateAssigns).toHaveBeenCalledWith('user1', assigns);
        });
    });

    describe('reply', () => {
        it('should call sendMessage on the channel engine with correct parameters', () => {
            const event = 'reply-event';
            const payload: PondMessage = { message: 'Reply message' };

            eventContext.reply(event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(
                'CHANNEL',
                ['user1'],
                ServerActions.SYSTEM,
                event,
                payload,
                'request-123',
            );
        });
    });

    describe('broadcast', () => {
        it('should call sendMessage on the channel engine with ALL_USERS', () => {
            const event = 'broadcast-event';
            const payload: PondMessage = { message: 'Broadcast message' };

            eventContext.broadcast(event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(
                'user1',
                'ALL_USERS',
                ServerActions.BROADCAST,
                event,
                payload,
                'request-123',
            );
        });
    });

    describe('broadcastFrom', () => {
        it('should call sendMessage on the channel engine with ALL_EXCEPT_SENDER', () => {
            const event = 'broadcast-from-event';
            const payload: PondMessage = { message: 'Broadcast from message' };

            eventContext.broadcastFrom(event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(
                'user1',
                'ALL_EXCEPT_SENDER',
                ServerActions.BROADCAST,
                event,
                payload,
                'request-123',
            );
        });
    });

    describe('broadcastTo', () => {
        it('should call sendMessage on the channel engine with specific user IDs', () => {
            const event = 'broadcast-to-event';
            const payload: PondMessage = { message: 'Broadcast to message' };
            const userIds = ['user2', 'user3'];

            eventContext.broadcastTo(event, payload, userIds);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith(
                'user1',
                userIds,
                ServerActions.BROADCAST,
                event,
                payload,
                'request-123',
            );
        });
    });

    describe('trackPresence', () => {
        it('should call trackPresence on the channel engine', () => {
            const presence: PondPresence = { status: 'online' };

            eventContext.trackPresence(presence);
            expect(mockChannelEngine.trackPresence).toHaveBeenCalledWith('user1', presence);
        });

        it('should call trackPresence with a specific user ID if provided', () => {
            const presence: PondPresence = { status: 'online' };
            const userId = 'user2';

            eventContext.trackPresence(presence, userId);
            expect(mockChannelEngine.trackPresence).toHaveBeenCalledWith(userId, presence);
        });
    });

    describe('updatePresence', () => {
        it('should call updatePresence on the channel engine', () => {
            const presence: PondPresence = { status: 'away' };

            eventContext.updatePresence(presence);
            expect(mockChannelEngine.updatePresence).toHaveBeenCalledWith('user1', presence);
        });

        it('should call updatePresence with a specific user ID if provided', () => {
            const presence: PondPresence = { status: 'away' };
            const userId = 'user2';

            eventContext.updatePresence(presence, userId);
            expect(mockChannelEngine.updatePresence).toHaveBeenCalledWith(userId, presence);
        });
    });

    describe('evictUser', () => {
        it('should call kickUser on the channel engine', () => {
            const reason = 'Violated rules';

            eventContext.evictUser(reason);
            expect(mockChannelEngine.kickUser).toHaveBeenCalledWith('user1', reason);
        });

        it('should call kickUser with a specific user ID if provided', () => {
            const reason = 'Violated rules';
            const userId = 'user2';

            eventContext.evictUser(reason, userId);
            expect(mockChannelEngine.kickUser).toHaveBeenCalledWith(userId, reason);
        });
    });

    describe('removePresence', () => {
        it('should call removePresence on the channel engine', () => {
            eventContext.removePresence();
            expect(mockChannelEngine.removePresence).toHaveBeenCalledWith('user1');
        });

        it('should call removePresence with a specific user ID if provided', () => {
            const userId = 'user2';

            eventContext.removePresence(userId);
            expect(mockChannelEngine.removePresence).toHaveBeenCalledWith(userId);
        });
    });
});
