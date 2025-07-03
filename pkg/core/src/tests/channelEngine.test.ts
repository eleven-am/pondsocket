import {
    ChannelReceiver,
    ClientActions,
    PondAssigns,
    PondPresence,
    ServerActions,
    SystemSender,
} from '@eleven-am/pondsocket-common';

import { ChannelEngine } from '../engines/channelEngine';
import { HttpError } from '../errors/httpError';
import { MockLobbyEngine } from './mocks/lobbyEngine';

const flushPromises = (value: number) => new Promise((resolve) => setTimeout(resolve, value));

describe('ChannelEngine', () => {
    let channelEngine: ChannelEngine;
    let mockLobbyEngine: MockLobbyEngine;
    const channelName = 'test-channel';

    beforeEach(() => {
        mockLobbyEngine = new MockLobbyEngine();
        channelEngine = new ChannelEngine(mockLobbyEngine, channelName);
    });

    afterEach(() => {
        jest.clearAllMocks();
    });

    describe('constructor', () => {
        it('should initialize with the correct name and parent', () => {
            expect(channelEngine.name).toBe(channelName);
            expect(channelEngine.parent).toBe(mockLobbyEngine);
        });

        it('should initialize with empty users set', () => {
            expect(channelEngine.users.size).toBe(0);
        });
    });

    describe('addUser', () => {
        it('should add a user successfully', () => {
            const userId = 'user1';
            const assigns: PondAssigns = { username: 'TestUser' };
            const onMessage = jest.fn();

            // We need to spy on sendMessage before calling addUser
            jest.spyOn(channelEngine, 'sendMessage');

            channelEngine.addUser(userId, assigns, onMessage);

            expect(channelEngine.users.has(userId)).toBe(true);
            expect(onMessage).toHaveBeenCalled();
        });

        it('should throw an error if user already exists', () => {
            const userId = 'user1';
            const assigns: PondAssigns = { username: 'TestUser' };
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);

            expect(() => {
                channelEngine.addUser(userId, assigns, onMessage);
            }).toThrow(HttpError);

            expect(() => {
                channelEngine.addUser(userId, assigns, onMessage);
            }).toThrow('User with id user1 already exists in channel test-channel');
        });

        it('should return an unsubscribe function that removes the user', () => {
            const userId = 'user1';
            const assigns: PondAssigns = { username: 'TestUser' };
            const onMessage = jest.fn();
            const removeUserSpy = jest.spyOn(channelEngine, 'removeUser');

            const unsubscribe = channelEngine.addUser(userId, assigns, onMessage);

            expect(typeof unsubscribe).toBe('function');

            unsubscribe();

            expect(removeUserSpy).toHaveBeenCalledWith(userId);
        });
    });

    describe('sendMessage', () => {
        it('should publish a message to the internal publisher', async () => {
            // Add a user first
            const userId = 'user1';
            const assigns: PondAssigns = { username: 'TestUser' };
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);

            // Send a message
            const event = 'test-event';
            const payload = { message: 'test message' };

            channelEngine.sendMessage(userId, ChannelReceiver.ALL_USERS, ServerActions.BROADCAST, event, payload);

            // Wait a bit to ensure the message is processed
            await flushPromises(1000);

            // Check that onMessage was called with the correct event
            expect(onMessage).toHaveBeenCalledWith(expect.objectContaining({
                channelName,
                action: ServerActions.BROADCAST,
                event,
                payload,
            }));
        });

        it('should throw an error if sender does not exist', () => {
            const sender = 'nonexistent-user';

            expect(() => {
                channelEngine.sendMessage(
                    sender,
                    ChannelReceiver.ALL_USERS,
                    ServerActions.BROADCAST,
                    'test',
                    {},
                );
            }).toThrow(HttpError);
        });

        it('should allow CHANNEL as a sender', async () => {
            // Add a user to receive the message
            const userId = 'user1';
            const assigns: PondAssigns = { username: 'TestUser' };
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);

            // Send a message from CHANNEL
            channelEngine.sendMessage(
                SystemSender.CHANNEL,
                ChannelReceiver.ALL_USERS,
                ServerActions.BROADCAST,
                'test-event',
                { message: 'test' },
            );

            // Wait a bit to ensure the message is processed
            await flushPromises(1000);

            // Check that message was sent
            expect(onMessage).toHaveBeenCalled();
        });
    });

    describe('broadcastMessage', () => {
        it('should process a client message through the middleware', () => {
            // Add a user
            const userId = 'user1';
            const assigns: PondAssigns = { username: 'TestUser' };
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);

            // Create a client message
            const clientMessage = {
                event: 'test-event',
                payload: { message: 'test message' },
                requestId: 'req-id',
                channelName,
                action: ClientActions.BROADCAST,
            };

            // Spy on middleware.run
            const middlewareRunSpy = jest.spyOn(mockLobbyEngine.middleware, 'run');

            // Broadcast the message
            channelEngine.broadcastMessage(userId, clientMessage);

            // Check that middleware.run was called
            expect(middlewareRunSpy).toHaveBeenCalledWith(
                expect.objectContaining({
                    sender: userId,
                    action: ServerActions.BROADCAST,
                    event: clientMessage.event,
                    payload: clientMessage.payload,
                }),
                channelEngine,
                expect.any(Function),
            );
        });

        it('should send an error if the user does not exist', () => {
            const userId = 'nonexistent-user';
            const clientMessage = {
                event: 'test-event',
                payload: { message: 'test message' },
                requestId: 'req-id',
                channelName,
                action: ClientActions.BROADCAST,
            };

            expect(() => {
                channelEngine.broadcastMessage(userId, clientMessage);
            }).toThrow(HttpError);
        });
    });

    describe('presence management', () => {
        const userId = 'user1';
        const presence: PondPresence = { status: 'online' };

        beforeEach(() => {
            // Add a user
            const assigns: PondAssigns = { username: 'TestUser' };
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);
        });

        it('should create a PresenceEngine on first presence operation', () => {
            // Skip trying to spy on private property

            // Track presence - this should create a PresenceEngine
            channelEngine.trackPresence(userId, presence);

            // GetPresence should return the presence data
            const userPresence = channelEngine.getPresence();

            expect(userPresence).toHaveProperty(userId);
            expect(userPresence[userId]).toEqual(presence);
        });

        it('should update presence correctly', () => {
            // First track presence
            channelEngine.trackPresence(userId, presence);

            // Now update it
            const newPresence: PondPresence = { status: 'away' };

            channelEngine.updatePresence(userId, newPresence);

            // Check that it was updated
            const userPresence = channelEngine.getPresence();

            expect(userPresence[userId]).toEqual(newPresence);
        });

        it('should remove presence correctly', () => {
            // First track presence
            channelEngine.trackPresence(userId, presence);

            // Now remove it
            channelEngine.removePresence(userId);

            // Check that it was removed
            const userPresence = channelEngine.getPresence();

            expect(userPresence).not.toHaveProperty(userId);
        });

        it('should upsert presence correctly', () => {
            // Upsert should add new presence
            channelEngine.upsertPresence(userId, presence);

            // Check it was added
            let userPresence = channelEngine.getPresence();

            expect(userPresence[userId]).toEqual(presence);

            // Upsert again should update
            const newPresence: PondPresence = { status: 'away' };

            channelEngine.upsertPresence(userId, newPresence);

            // Check it was updated
            userPresence = channelEngine.getPresence();
            expect(userPresence[userId]).toEqual(newPresence);
        });
    });

    describe('assigns management', () => {
        const userId = 'user1';
        const initialAssigns: PondAssigns = { role: 'user' };

        beforeEach(() => {
            // Add a user
            const onMessage = jest.fn();

            channelEngine.addUser(userId, initialAssigns, onMessage);
        });

        it('should store initial assigns when adding a user', () => {
            const assigns = channelEngine.getAssigns();

            expect(assigns).toHaveProperty(userId);
            expect(assigns[userId]).toEqual(initialAssigns);
        });

        it('should update assigns correctly', () => {
            // Update assigns
            const updateAssigns: PondAssigns = { role: 'admin' };

            channelEngine.updateAssigns(userId, updateAssigns);

            // Check they were updated and merged
            const assigns = channelEngine.getAssigns();

            expect(assigns[userId]).toEqual({ ...initialAssigns,
                ...updateAssigns });
        });

        it('should throw error when updating assigns for non-existent user', () => {
            expect(() => {
                channelEngine.updateAssigns('nonexistent', { role: 'admin' });
            }).toThrow(HttpError);
        });
    });

    describe('getUserData', () => {
        const userId = 'user1';
        const assigns: PondAssigns = { role: 'user' };
        const presence: PondPresence = { status: 'online' };

        beforeEach(() => {
            // Add a user
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);
        });

        it('should return user data with assigns', () => {
            const userData = channelEngine.getUserData(userId);

            expect(userData).toEqual({
                id: userId,
                assigns,
                presence: {},
            });
        });

        it('should return user data with presence if tracked', () => {
            // Track presence
            channelEngine.trackPresence(userId, presence);

            const userData = channelEngine.getUserData(userId);

            expect(userData).toEqual({
                id: userId,
                assigns,
                presence,
            });
        });

        it('should throw error for non-existent user', () => {
            expect(() => {
                channelEngine.getUserData('nonexistent');
            }).toThrow(HttpError);
        });
    });

    describe('kickUser', () => {
        const userId = 'user1';

        beforeEach(() => {
            // Add a user
            const assigns: PondAssigns = { role: 'user' };
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);
        });

        it('should send kicked_out message to the user', () => {
            // We need to check this behavior in a different way
            // Mock the sendMessage method
            const sendMessageMock = jest.fn();

            channelEngine.sendMessage = sendMessageMock;

            const reason = 'Bad behavior';

            channelEngine.kickUser(userId, reason);

            // Check first call is kicked_out
            expect(sendMessageMock.mock.calls[0][0]).toBe(SystemSender.CHANNEL);
            expect(sendMessageMock.mock.calls[0][1]).toEqual([userId]);
            expect(sendMessageMock.mock.calls[0][2]).toBe(ServerActions.SYSTEM);
            expect(sendMessageMock.mock.calls[0][3]).toBe('kicked_out');
            expect(sendMessageMock.mock.calls[0][4]).toEqual({
                message: reason,
                code: 403,
            });
        });

        it('should broadcast kicked message to all users', () => {
            // Mock the sendMessage method
            const sendMessageMock = jest.fn();

            channelEngine.sendMessage = sendMessageMock;

            const reason = 'Bad behavior';

            channelEngine.kickUser(userId, reason);

            // Check second call is kicked broadcast
            expect(sendMessageMock.mock.calls[1][0]).toBe(SystemSender.CHANNEL);
            expect(sendMessageMock.mock.calls[1][1]).toBe(ChannelReceiver.ALL_USERS);
            expect(sendMessageMock.mock.calls[1][2]).toBe(ServerActions.SYSTEM);
            expect(sendMessageMock.mock.calls[1][3]).toBe('kicked');
            expect(sendMessageMock.mock.calls[1][4]).toEqual({
                userId,
                reason,
            });
        });

        it('should remove the user from the channel', () => {
            const removeUserSpy = jest.spyOn(channelEngine, 'removeUser');
            const reason = 'Bad behavior';

            channelEngine.kickUser(userId, reason);

            expect(removeUserSpy).toHaveBeenCalledWith(userId);
        });
    });

    describe('removeUser', () => {
        const userId = 'user1';
        const assigns: PondAssigns = { role: 'user' };
        const presence: PondPresence = { status: 'online' };
        let onMessage: jest.Mock;

        beforeEach(() => {
            // Add a user
            onMessage = jest.fn();
            channelEngine.addUser(userId, assigns, onMessage);

            // Add presence
            channelEngine.trackPresence(userId, presence);
        });

        it('should remove the user from assigns and presence', () => {
            channelEngine.removeUser(userId);

            // Check user was removed
            expect(channelEngine.users.has(userId)).toBe(false);

            // Presence should be removed too
            const presences = channelEngine.getPresence();

            expect(presences).not.toHaveProperty(userId);
        });

        it('should trigger leave callback if set', () => {
            // Set up a leave callback
            const leaveCallback = jest.fn();

            mockLobbyEngine.leaveCallback = leaveCallback;

            // Create a channel wrapper spy
            const wrapChannelSpy = jest.spyOn(mockLobbyEngine, 'wrapChannel');

            channelEngine.removeUser(userId);

            // Check callback was triggered with correct data
            expect(leaveCallback).toHaveBeenCalledWith(expect.objectContaining({
                user: expect.objectContaining({
                    id: userId,
                    assigns,
                    presence,
                }),
                channel: expect.any(Object),
            }));

            // Check the channel was wrapped
            expect(wrapChannelSpy).toHaveBeenCalledWith(channelEngine);
        });

        it('should not error when removing a non-existent user', () => {
            expect(() => {
                channelEngine.removeUser('nonexistent');
            }).not.toThrow();
        });
    });

    describe('destroy', () => {
        it('should send destroyed message to all users', () => {
            // Replace the sendMessage method with a mock
            const sendMessageMock = jest.fn();

            channelEngine.sendMessage = sendMessageMock;

            const reason = 'Channel closed';

            channelEngine.destroy(reason);

            // Check message parameters directly
            expect(sendMessageMock).toHaveBeenCalled();
            expect(sendMessageMock.mock.calls[0][0]).toBe(SystemSender.CHANNEL);
            expect(sendMessageMock.mock.calls[0][1]).toBe(ChannelReceiver.ALL_USERS);
            expect(sendMessageMock.mock.calls[0][2]).toBe(ServerActions.ERROR);
            expect(sendMessageMock.mock.calls[0][3]).toBe('destroyed');
            expect(sendMessageMock.mock.calls[0][4]).toEqual({ message: reason });
        });

        it('should use default message if no reason provided', () => {
            // Replace the sendMessage method with a mock
            const sendMessageMock = jest.fn();

            channelEngine.sendMessage = sendMessageMock;

            channelEngine.destroy();

            // Check defaults
            expect(sendMessageMock).toHaveBeenCalled();
            expect(sendMessageMock.mock.calls[0][4]).toEqual({
                message: 'Channel has been destroyed',
            });
        });

        it('should close the channel', () => {
            // Using a different approach since we can't spy on private methods
            // Mock the close method
            const deleteChannel = jest.spyOn(mockLobbyEngine as any, 'deleteChannel').mockImplementation(() => {});

            channelEngine.destroy();

            expect(deleteChannel).toHaveBeenCalledWith(channelEngine.name);
        });
    });

    describe('close', () => {
        const userId = 'user1';
        const assigns: PondAssigns = { role: 'user' };
        const presence: PondPresence = { status: 'online' };

        beforeEach(() => {
            // Add a user with presence
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);
            channelEngine.trackPresence(userId, presence);
        });

        it('should clear all user data', () => {
            // Call close using the public interface
            // Need a special approach since it's private
            (channelEngine as any).close();

            // Users should be empty
            expect(channelEngine.users.size).toBe(0);

            // Presence should be empty
            expect(Object.keys(channelEngine.getPresence())).toHaveLength(0);
        });
    });
});
