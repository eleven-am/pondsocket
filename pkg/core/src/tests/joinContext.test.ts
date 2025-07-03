import {
    PondEvent,
    JoinParams,
    ServerActions,
    PondPresence,
    PondAssigns,
    PondMessage,
    ErrorTypes,
} from '@eleven-am/pondsocket-common';

import { JoinRequestOptions, RequestCache } from '../abstracts/types';
import { JoinContext } from '../contexts/joinContext';
import { HttpError } from '../errors/httpError';
import { Channel } from '../wrappers/channel';
import { MockChannelEngine } from './mocks/channelEnegine';

describe('JoinContext', () => {
    let mockChannelEngine: MockChannelEngine;
    let joinContext: JoinContext<string>;
    const mockOptions: JoinRequestOptions<string> = {
        clientId: 'user1',
        assigns: { role: 'user' },
        joinParams: { token: 'abc123' } as JoinParams,
        params: {
            params: { id: '123' },
            query: { sort: 'asc' },
        },
    };
    let mockUser: RequestCache;

    beforeEach(() => {
        mockChannelEngine = new MockChannelEngine();
        mockChannelEngine.name = 'test-channel';
        mockUser = {
            clientId: 'user1',
            socket: {} as any,
            assigns: { role: 'user' },
            subscriptions: new Set(),
            channelName: 'test-channel',
            requestId: 'request-123',
        };
        joinContext = new JoinContext(mockOptions, mockChannelEngine, mockUser);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    // JoinRequest tests
    describe('event', () => {
        it('should return the correct PondEvent object', () => {
            const expectedEvent: PondEvent<string> = {
                event: 'test-channel',
                params: { id: '123' },
                query: { sort: 'asc' },
                payload: { token: 'abc123' },
            };

            expect(joinContext.event).toEqual(expectedEvent);
        });
    });

    describe('channelName', () => {
        it('should return the correct channel name', () => {
            expect(joinContext.channelName).toBe('test-channel');
        });
    });

    describe('channel', () => {
        it('should return a Channel instance', () => {
            const channel = joinContext.channel;

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

            expect(joinContext.presences).toEqual(mockPresences);
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

            expect(joinContext.assigns).toEqual(mockAssigns);
            expect(mockChannelEngine.getAssigns).toHaveBeenCalled();
        });
    });

    describe('user', () => {
        it('should return the user data from the join options', () => {
            const expectedUserData = {
                id: 'user1',
                assigns: { role: 'user' },
                presence: {},
            };

            expect(joinContext.user).toEqual(expectedUserData);
        });
    });

    describe('joinParams', () => {
        it('should return the join parameters from the options', () => {
            expect(joinContext.joinParams).toEqual({ token: 'abc123' });
        });
    });

    // JoinResponse tests
    describe('hasResponded', () => {
        it('should return false initially', () => {
            expect(joinContext.hasResponded).toBe(false);
        });

        it('should return true after accept', () => {
            joinContext.accept();
            expect(joinContext.hasResponded).toBe(true);
        });

        it('should return true after decline', () => {
            joinContext.decline();
            expect(joinContext.hasResponded).toBe(true);
        });
    });

    describe('accept', () => {
        it('should add user to the channel and mark as accepted', () => {
            const addUserSpy = jest.spyOn(mockChannelEngine, 'addUser');

            joinContext.accept();
            expect(addUserSpy).toHaveBeenCalledWith('user1', { role: 'user' }, expect.any(Function));
        });

        it('should throw an error if already executed', () => {
            joinContext.accept();
            expect(() => joinContext.accept()).toThrow(HttpError);
        });
    });

    describe('decline', () => {
        it('should send an error message and mark as executed', () => {
            const sendMessageSpy = jest.spyOn(mockChannelEngine.parent.parent, 'sendMessage');

            joinContext.decline('Unauthorized', 401);
            expect(sendMessageSpy).toHaveBeenCalledWith({} as any, expect.objectContaining({
                event: ErrorTypes.UNAUTHORIZED_JOIN_REQUEST,
                action: ServerActions.ERROR,
                payload: {
                    message: 'Unauthorized',
                    code: 401,
                },
            }));
        });

        it('should throw an error if already executed', () => {
            joinContext.decline();
            expect(() => joinContext.decline()).toThrow(HttpError);
        });
    });

    describe('assign', () => {
        it('should update assigns if not accepted', () => {
            const newAssigns: PondAssigns = { role: 'admin' };

            joinContext.assign(newAssigns);
            joinContext.accept();
            expect(mockChannelEngine.addUser).toHaveBeenCalledWith('user1', { role: 'admin' }, expect.any(Function));
        });

        it('should call updateAssigns on channel engine if already accepted', () => {
            joinContext.accept();
            const newAssigns: PondAssigns = { role: 'admin' };

            joinContext.assign(newAssigns);
            expect(mockChannelEngine.updateAssigns).toHaveBeenCalledWith('user1', newAssigns);
        });
    });

    describe('reply', () => {
        it('should send a message to the user', () => {
            const sendMessageSpy = jest.spyOn(mockChannelEngine.parent.parent, 'sendMessage');
            const event = 'test-event';
            const payload: PondMessage = { message: 'Test message' };

            joinContext.reply(event, payload);
            expect(sendMessageSpy).toHaveBeenCalledWith({} as any, expect.objectContaining({
                event,
                action: ServerActions.SYSTEM,
                payload,
            }));
        });
    });

    describe('broadcastTo', () => {
        it('should call sendMessage on channel engine with specific user IDs', () => {
            const event = 'broadcast-to-event';
            const payload: PondMessage = { message: 'Broadcast to message' };
            const userIds = ['user2', 'user3'];

            joinContext.broadcastTo(event, payload, userIds);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith('user1', userIds, ServerActions.BROADCAST, event, payload, 'request-123');
        });
    });

    describe('broadcast', () => {
        it('should call sendMessage on channel engine with ALL_USERS', () => {
            const event = 'broadcast-event';
            const payload: PondMessage = { message: 'Broadcast message' };

            joinContext.broadcast(event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith('user1', 'ALL_USERS', ServerActions.BROADCAST, event, payload, 'request-123');
        });
    });

    describe('broadcastFrom', () => {
        it('should call sendMessage on channel engine with ALL_EXCEPT_SENDER', () => {
            const event = 'broadcast-from-event';
            const payload: PondMessage = { message: 'Broadcast from message' };

            joinContext.broadcastFrom(event, payload);
            expect(mockChannelEngine.sendMessage).toHaveBeenCalledWith('user1', 'ALL_EXCEPT_SENDER', ServerActions.BROADCAST, event, payload, 'request-123');
        });
    });

    describe('trackPresence', () => {
        it('should call trackPresence on channel engine', () => {
            const presence: PondPresence = { status: 'online' };

            joinContext.trackPresence(presence);
            expect(mockChannelEngine.trackPresence).toHaveBeenCalledWith('user1', presence);
        });
    });
});
