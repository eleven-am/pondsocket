import {
    ChannelReceiver,
    ClientActions,
    PondAssigns,
    PondPresence,
    ServerActions,
    SystemSender,
    Unsubscribe,
} from '@eleven-am/pondsocket-common';

import { ChannelEngine } from '../engines/channelEngine';
import { HttpError } from '../errors/httpError';
import {
    DistributedChannelMessage,
    DistributedMessageType,
    IDistributedBackend,
} from '../types';
import { MockLobbyEngine } from './mocks/lobbyEngine';

class MockDistributedBackend implements IDistributedBackend {
    readonly nodeId = 'mock-node-id';
    readonly heartbeatTimeoutMs = 500;
    broadcastCalls: DistributedChannelMessage[] = [];
    private subscribeHandler: ((message: DistributedChannelMessage) => void) | null = null;
    private heartbeatHandler: ((nodeId: string) => void) | null = null;

    async initialize (): Promise<void> {}

    async broadcast (_endpointName: string, _channelName: string, message: DistributedChannelMessage): Promise<void> {
        this.broadcastCalls.push(message);
    }

    async subscribeToChannel (_endpointName: string, _channelName: string, handler: (message: DistributedChannelMessage) => void): Promise<Unsubscribe> {
        this.subscribeHandler = handler;

        return () => {
            this.subscribeHandler = null;
        };
    }

    subscribeToHeartbeats (handler: (nodeId: string) => void): Unsubscribe {
        this.heartbeatHandler = handler;

        return () => {
            this.heartbeatHandler = null;
        };
    }

    async cleanup (): Promise<void> {}

    simulateMessage (message: DistributedChannelMessage): void {
        if (this.subscribeHandler) {
            this.subscribeHandler(message);
        }
    }

    simulateHeartbeat (nodeId: string): void {
        if (this.heartbeatHandler) {
            this.heartbeatHandler(nodeId);
        }
    }
}

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

            expect(removeUserSpy).toHaveBeenCalledWith(userId, true);
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
            const onMessage = jest.fn();

            channelEngine.addUser(userId, assigns, onMessage);
            channelEngine.trackPresence(userId, presence);
        });

        it('should clear all user data', () => {
            (channelEngine as any).close();

            expect(channelEngine.users.size).toBe(0);
            expect(Object.keys(channelEngine.getPresence())).toHaveLength(0);
        });
    });

    describe('sendMessage edge cases', () => {
        beforeEach(() => {
            channelEngine.addUser('user1', { role: 'user' }, jest.fn());
            channelEngine.addUser('user2', { role: 'user' }, jest.fn());
        });

        it('should throw when ALL_EXCEPT_SENDER is used with CHANNEL sender', () => {
            expect(() => {
                channelEngine.sendMessage(
                    SystemSender.CHANNEL,
                    ChannelReceiver.ALL_EXCEPT_SENDER,
                    ServerActions.BROADCAST,
                    'test',
                    {},
                );
            }).toThrow(HttpError);
        });

        it('should throw when recipients contain invalid user ids', () => {
            expect(() => {
                channelEngine.sendMessage(
                    'user1',
                    ['user1', 'nonexistent'],
                    ServerActions.BROADCAST,
                    'test',
                    {},
                );
            }).toThrow(HttpError);
        });

        it('should send to specific user ids', () => {
            const onMessage1 = jest.fn();
            const mockLobby = new MockLobbyEngine();
            const engine = new ChannelEngine(mockLobby, 'ch');

            engine.addUser('a', {}, onMessage1);
            engine.addUser('b', {}, jest.fn());

            expect(() => {
                engine.sendMessage('a', ['a'], ServerActions.BROADCAST, 'test', {});
            }).not.toThrow();
        });
    });

    describe('removePresence when no presence engine exists', () => {
        it('should not throw when removing presence with no presence engine', () => {
            channelEngine.addUser('user1', {}, jest.fn());

            expect(() => {
                channelEngine.removePresence('user1');
            }).not.toThrow();
        });
    });

    describe('getPresence with no presence engine', () => {
        it('should return empty object', () => {
            expect(channelEngine.getPresence()).toEqual({});
        });
    });

    describe('getAssigns with multiple users', () => {
        it('should return assigns for all users', () => {
            channelEngine.addUser('user1', { role: 'admin' }, jest.fn());
            channelEngine.addUser('user2', { role: 'user' }, jest.fn());

            const assigns = channelEngine.getAssigns();

            expect(assigns).toEqual({
                user1: { role: 'admin' },
                user2: { role: 'user' },
            });
        });
    });

    describe('removeUser cleans up last user', () => {
        it('should close the channel when the last user is removed', () => {
            const deleteChannelSpy = jest.spyOn(mockLobbyEngine as any, 'deleteChannel').mockImplementation(() => {});

            channelEngine.addUser('solo', {}, jest.fn());
            channelEngine.removeUser('solo');

            expect(deleteChannelSpy).toHaveBeenCalledWith(channelName);
        });
    });

    describe('broadcastMessage error handling', () => {
        it('should invoke middleware final callback with error info when no handler matches', () => {
            channelEngine.addUser('user1', {}, jest.fn());

            mockLobbyEngine.middleware.run = jest.fn((_event, _channel, final) => {
                final();
            });

            const sendMessageSpy = jest.spyOn(channelEngine, 'sendMessage');

            channelEngine.broadcastMessage('user1', {
                event: 'unhandled',
                payload: {},
                requestId: 'req-1',
                channelName: 'test-channel',
                action: ClientActions.BROADCAST,
            });

            expect(sendMessageSpy).toHaveBeenCalledWith(
                SystemSender.CHANNEL,
                ['user1'],
                ServerActions.ERROR,
                expect.any(String),
                expect.objectContaining({ message: expect.any(String) }),
                'req-1',
            );
        });
    });
});

describe('ChannelEngine distributed', () => {
    let mockBackend: MockDistributedBackend;
    let mockLobbyEngine: MockLobbyEngine;
    let channelEngine: ChannelEngine;
    const channelName = 'dist-channel';

    beforeEach(async () => {
        jest.useFakeTimers({ doNotFake: ['nextTick'] });
        mockBackend = new MockDistributedBackend();
        mockLobbyEngine = new MockLobbyEngine();
        channelEngine = new ChannelEngine(mockLobbyEngine, channelName, mockBackend);
        await new Promise(process.nextTick);
    });

    afterEach(() => {
        channelEngine.close();
        jest.useRealTimers();
        jest.clearAllMocks();
    });

    describe('constructor with backend', () => {
        it('should set up distributed subscription and heartbeat tracking', () => {
            expect(channelEngine.name).toBe(channelName);
        });
    });

    describe('addUser with backend', () => {
        it('should broadcast USER_JOINED to other nodes', async () => {
            const onMessage = jest.fn();

            channelEngine.addUser('user1', { role: 'admin' }, onMessage);

            await Promise.resolve();

            const joinMsg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.USER_JOINED,
            );

            expect(joinMsg).toBeDefined();
            expect((joinMsg as any).userId).toBe('user1');
            expect((joinMsg as any).assigns).toEqual({ role: 'admin' });
        });

        it('should request channel state when first user joins', async () => {
            const onMessage = jest.fn();

            channelEngine.addUser('user1', {}, onMessage);

            await Promise.resolve();

            const stateReq = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.STATE_REQUEST,
            );

            expect(stateReq).toBeDefined();
        });
    });

    describe('removeUser with backend', () => {
        it('should broadcast USER_LEFT to other nodes', async () => {
            const onMessage = jest.fn();

            channelEngine.addUser('user1', {}, onMessage);
            mockBackend.broadcastCalls = [];
            channelEngine.addUser('user2', {}, jest.fn());
            mockBackend.broadcastCalls = [];

            channelEngine.removeUser('user1');

            await Promise.resolve();

            const leftMsg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.USER_LEFT,
            );

            expect(leftMsg).toBeDefined();
            expect((leftMsg as any).userId).toBe('user1');
        });

        it('should not broadcast USER_LEFT when skipDistributedBroadcast is true', async () => {
            const onMessage = jest.fn();

            channelEngine.addUser('user1', {}, onMessage);
            channelEngine.addUser('user2', {}, jest.fn());
            mockBackend.broadcastCalls = [];

            channelEngine.removeUser('user1', true);

            await Promise.resolve();

            const leftMsg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.USER_LEFT,
            );

            expect(leftMsg).toBeUndefined();
        });
    });

    describe('presence with backend', () => {
        beforeEach(() => {
            channelEngine.addUser('user1', {}, jest.fn());
            mockBackend.broadcastCalls = [];
        });

        it('should broadcast PRESENCE_UPDATE on trackPresence', async () => {
            channelEngine.trackPresence('user1', { status: 'online' });

            await Promise.resolve();

            const msg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.PRESENCE_UPDATE,
            );

            expect(msg).toBeDefined();
            expect((msg as any).userId).toBe('user1');
            expect((msg as any).presence).toEqual({ status: 'online' });
        });

        it('should broadcast PRESENCE_UPDATE on updatePresence', async () => {
            channelEngine.trackPresence('user1', { status: 'online' });
            mockBackend.broadcastCalls = [];

            channelEngine.updatePresence('user1', { status: 'away' });

            await Promise.resolve();

            const msg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.PRESENCE_UPDATE,
            );

            expect(msg).toBeDefined();
            expect((msg as any).presence).toEqual({ status: 'away' });
        });

        it('should broadcast PRESENCE_REMOVED on removePresence', async () => {
            channelEngine.trackPresence('user1', { status: 'online' });
            mockBackend.broadcastCalls = [];

            channelEngine.removePresence('user1');

            await Promise.resolve();

            const msg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.PRESENCE_REMOVED,
            );

            expect(msg).toBeDefined();
            expect((msg as any).userId).toBe('user1');
        });

        it('should broadcast PRESENCE_UPDATE on upsertPresence', async () => {
            channelEngine.upsertPresence('user1', { status: 'online' });

            await Promise.resolve();

            const msg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.PRESENCE_UPDATE,
            );

            expect(msg).toBeDefined();
        });
    });

    describe('assigns with backend', () => {
        beforeEach(() => {
            channelEngine.addUser('user1', { role: 'user' }, jest.fn());
            mockBackend.broadcastCalls = [];
        });

        it('should broadcast ASSIGNS_UPDATE on updateAssigns', async () => {
            channelEngine.updateAssigns('user1', { role: 'admin' });

            await Promise.resolve();

            const msg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.ASSIGNS_UPDATE,
            );

            expect(msg).toBeDefined();
            expect((msg as any).assigns).toEqual({ role: 'admin' });
        });
    });

    describe('kickUser with backend', () => {
        it('should broadcast EVICT_USER to other nodes', async () => {
            channelEngine.addUser('user1', {}, jest.fn());
            mockBackend.broadcastCalls = [];

            const sendMessageMock = jest.fn();

            channelEngine.sendMessage = sendMessageMock;
            channelEngine.kickUser('user1', 'bad behavior');

            await Promise.resolve();

            const msg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.EVICT_USER,
            );

            expect(msg).toBeDefined();
            expect((msg as any).userId).toBe('user1');
            expect((msg as any).reason).toBe('bad behavior');
        });
    });

    describe('sendMessage with backend', () => {
        it('should broadcast USER_MESSAGE to other nodes', async () => {
            channelEngine.addUser('user1', {}, jest.fn());
            mockBackend.broadcastCalls = [];

            channelEngine.sendMessage(
                'user1',
                ChannelReceiver.ALL_USERS,
                ServerActions.BROADCAST,
                'test-event',
                { data: 'hello' },
            );

            await Promise.resolve();

            const msg = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.USER_MESSAGE,
            );

            expect(msg).toBeDefined();
            expect((msg as any).event).toBe('test-event');
            expect((msg as any).payload).toEqual({ data: 'hello' });
            expect((msg as any).recipientDescriptor).toBe(ChannelReceiver.ALL_USERS);
        });
    });

    describe('handleDistributedMessage - STATE_REQUEST', () => {
        it('should respond with STATE_RESPONSE when channel has users', async () => {
            channelEngine.addUser('user1', { role: 'admin' }, jest.fn());
            channelEngine.trackPresence('user1', { status: 'online' });
            mockBackend.broadcastCalls = [];

            mockBackend.simulateMessage({
                type: DistributedMessageType.STATE_REQUEST,
                endpointName: 'String',
                channelName,
                fromNode: 'other-node',
            });

            await Promise.resolve();

            const response = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.STATE_RESPONSE,
            );

            expect(response).toBeDefined();
            expect((response as any).users).toHaveLength(1);
            expect((response as any).users[0].id).toBe('user1');
            expect((response as any).users[0].assigns).toEqual({ role: 'admin' });
            expect((response as any).users[0].presence).toEqual({ status: 'online' });
        });

        it('should not respond when channel has no users', async () => {
            mockBackend.broadcastCalls = [];

            mockBackend.simulateMessage({
                type: DistributedMessageType.STATE_REQUEST,
                endpointName: 'String',
                channelName,
                fromNode: 'other-node',
            });

            await Promise.resolve();

            const response = mockBackend.broadcastCalls.find(
                (m) => m.type === DistributedMessageType.STATE_RESPONSE,
            );

            expect(response).toBeUndefined();
        });
    });

    describe('handleDistributedMessage - STATE_RESPONSE', () => {
        it('should merge remote users into local state', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.STATE_RESPONSE,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                users: [
                    { id: 'remote-user', assigns: { role: 'viewer' }, presence: { status: 'online' } },
                ],
            });

            expect(channelEngine.users.has('remote-user')).toBe(true);
            const assigns = channelEngine.getAssigns();

            expect(assigns['remote-user']).toEqual({ role: 'viewer' });

            const presence = channelEngine.getPresence();

            expect(presence['remote-user']).toEqual({ status: 'online' });
        });

        it('should not override existing local users', () => {
            channelEngine.addUser('user1', { role: 'admin' }, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.STATE_RESPONSE,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                users: [
                    { id: 'user1', assigns: { role: 'viewer' }, presence: {} },
                ],
            });

            const assigns = channelEngine.getAssigns();

            expect(assigns['user1']).toEqual({ role: 'admin' });
        });

        it('should handle empty users array', () => {
            mockBackend.simulateMessage({
                type: DistributedMessageType.STATE_RESPONSE,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                users: [],
            });

            expect(channelEngine.users.size).toBe(0);
        });

        it('should handle users without presence', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.STATE_RESPONSE,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                users: [
                    { id: 'remote-user', assigns: { role: 'viewer' }, presence: {} },
                ],
            });

            expect(channelEngine.users.has('remote-user')).toBe(true);
            const presence = channelEngine.getPresence();

            expect(presence['remote-user']).toBeUndefined();
        });
    });

    describe('handleDistributedMessage - USER_JOINED', () => {
        it('should add remote user to local state', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
                assigns: { role: 'user' },
                presence: { status: 'online' },
            });

            expect(channelEngine.users.has('remote-user')).toBe(true);
            const assigns = channelEngine.getAssigns();

            expect(assigns['remote-user']).toEqual({ role: 'user' });

            const presence = channelEngine.getPresence();

            expect(presence['remote-user']).toEqual({ status: 'online' });
        });

        it('should not add user if already exists locally', () => {
            channelEngine.addUser('user1', { role: 'admin' }, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'user1',
                assigns: { role: 'viewer' },
                presence: {},
            });

            const assigns = channelEngine.getAssigns();

            expect(assigns['user1']).toEqual({ role: 'admin' });
        });

        it('should add user without presence when presence is empty', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
                assigns: { role: 'user' },
                presence: {},
            });

            expect(channelEngine.users.has('remote-user')).toBe(true);
        });
    });

    describe('handleDistributedMessage - USER_LEFT', () => {
        it('should remove remote user from local state', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
                assigns: { role: 'user' },
                presence: { status: 'online' },
            });

            expect(channelEngine.users.has('remote-user')).toBe(true);

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_LEFT,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
            });

            expect(channelEngine.users.has('remote-user')).toBe(false);
        });

        it('should handle left for user without presence', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
                assigns: {},
                presence: {},
            });

            expect(() => {
                mockBackend.simulateMessage({
                    type: DistributedMessageType.USER_LEFT,
                    endpointName: 'String',
                    channelName,
                    sourceNodeId: 'remote-node',
                    userId: 'remote-user',
                });
            }).not.toThrow();
        });
    });

    describe('handleDistributedMessage - USER_MESSAGE', () => {
        it('should deliver remote messages to local users', async () => {
            const onMessage = jest.fn();

            channelEngine.addUser('local-user', {}, onMessage);

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: 'String',
                channelName,
                fromUserId: 'remote-sender',
                event: 'chat',
                payload: { text: 'hello' },
                requestId: 'req-123',
                recipientDescriptor: ChannelReceiver.ALL_USERS,
            });

            await new Promise(process.nextTick);

            expect(onMessage).toHaveBeenCalledWith(
                expect.objectContaining({
                    event: 'chat',
                    payload: { text: 'hello' },
                }),
            );
        });

        it('should filter messages using ALL_EXCEPT_SENDER', async () => {
            const onMessage1 = jest.fn();
            const onMessage2 = jest.fn();

            channelEngine.addUser('user1', {}, onMessage1);
            channelEngine.addUser('user2', {}, onMessage2);

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: 'String',
                channelName,
                fromUserId: 'user1',
                event: 'chat',
                payload: { text: 'hello' },
                requestId: 'req-123',
                recipientDescriptor: ChannelReceiver.ALL_EXCEPT_SENDER,
            });

            await new Promise(process.nextTick);

            expect(onMessage1).not.toHaveBeenCalledWith(
                expect.objectContaining({ event: 'chat' }),
            );
            expect(onMessage2).toHaveBeenCalledWith(
                expect.objectContaining({ event: 'chat' }),
            );
        });

        it('should filter messages using specific recipient array', async () => {
            const onMessage1 = jest.fn();
            const onMessage2 = jest.fn();

            channelEngine.addUser('user1', {}, onMessage1);
            channelEngine.addUser('user2', {}, onMessage2);

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: 'String',
                channelName,
                fromUserId: 'remote-sender',
                event: 'dm',
                payload: { text: 'private' },
                requestId: 'req-456',
                recipientDescriptor: ['user1'],
            });

            await new Promise(process.nextTick);

            expect(onMessage1).toHaveBeenCalledWith(
                expect.objectContaining({ event: 'dm' }),
            );
            expect(onMessage2).not.toHaveBeenCalledWith(
                expect.objectContaining({ event: 'dm' }),
            );
        });

        it('should not deliver when no local recipients match', () => {
            const onMessage = jest.fn();

            channelEngine.addUser('user1', {}, onMessage);

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_MESSAGE,
                endpointName: 'String',
                channelName,
                fromUserId: 'remote-sender',
                event: 'dm',
                payload: {},
                requestId: 'req-999',
                recipientDescriptor: ['non-existent-user'],
            });

            expect(onMessage).not.toHaveBeenCalledWith(
                expect.objectContaining({ event: 'dm' }),
            );
        });
    });

    describe('handleDistributedMessage - PRESENCE_UPDATE', () => {
        it('should upsert remote user presence', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.PRESENCE_UPDATE,
                endpointName: 'String',
                channelName,
                userId: 'remote-user',
                presence: { status: 'online' },
            });

            const presence = channelEngine.getPresence();

            expect(presence['remote-user']).toEqual({ status: 'online' });
        });
    });

    describe('handleDistributedMessage - PRESENCE_REMOVED', () => {
        it('should remove remote user presence', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.PRESENCE_UPDATE,
                endpointName: 'String',
                channelName,
                userId: 'remote-user',
                presence: { status: 'online' },
            });

            expect(channelEngine.getPresence()['remote-user']).toEqual({ status: 'online' });

            mockBackend.simulateMessage({
                type: DistributedMessageType.PRESENCE_REMOVED,
                endpointName: 'String',
                channelName,
                userId: 'remote-user',
            });

            expect(channelEngine.getPresence()['remote-user']).toBeUndefined();
        });

        it('should not throw when removing presence without presence engine', () => {
            expect(() => {
                mockBackend.simulateMessage({
                    type: DistributedMessageType.PRESENCE_REMOVED,
                    endpointName: 'String',
                    channelName,
                    userId: 'unknown-user',
                });
            }).not.toThrow();
        });
    });

    describe('handleDistributedMessage - ASSIGNS_UPDATE', () => {
        it('should update remote user assigns', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
                assigns: { role: 'user' },
                presence: {},
            });

            mockBackend.simulateMessage({
                type: DistributedMessageType.ASSIGNS_UPDATE,
                endpointName: 'String',
                channelName,
                userId: 'remote-user',
                assigns: { role: 'admin' },
            });

            const assigns = channelEngine.getAssigns();

            expect(assigns['remote-user']).toEqual({ role: 'admin' });
        });
    });

    describe('handleDistributedMessage - EVICT_USER', () => {
        it('should evict a local user when receiving remote eviction', async () => {
            const onMessage = jest.fn();

            channelEngine.addUser('user1', {}, onMessage);
            channelEngine.addUser('user2', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.EVICT_USER,
                endpointName: 'String',
                channelName,
                userId: 'user1',
                reason: 'bad behavior',
            });

            await new Promise(process.nextTick);

            expect(onMessage).toHaveBeenCalledWith(
                expect.objectContaining({
                    event: 'kicked_out',
                    payload: expect.objectContaining({ message: 'bad behavior' }),
                }),
            );
        });

        it('should handle eviction for non-local user', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
                assigns: { role: 'user' },
                presence: { status: 'online' },
            });

            expect(() => {
                mockBackend.simulateMessage({
                    type: DistributedMessageType.EVICT_USER,
                    endpointName: 'String',
                    channelName,
                    userId: 'remote-user',
                    reason: 'evicted',
                });
            }).not.toThrow();

            expect(channelEngine.users.has('remote-user')).toBe(false);
        });
    });

    describe('handleDistributedMessage - NODE_HEARTBEAT', () => {
        it('should not throw on heartbeat message', () => {
            expect(() => {
                mockBackend.simulateMessage({
                    type: DistributedMessageType.NODE_HEARTBEAT,
                    endpointName: '__heartbeat__',
                    channelName: '__heartbeat__',
                    nodeId: 'some-node',
                });
            }).not.toThrow();
        });
    });

    describe('heartbeat tracking and stale node cleanup', () => {
        it('should track heartbeats from other nodes', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'remote-node',
                userId: 'remote-user',
                assigns: { role: 'user' },
                presence: {},
            });

            mockBackend.simulateHeartbeat('remote-node');

            expect(channelEngine.users.has('remote-user')).toBe(true);
        });

        it('should clean up users from stale nodes', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'stale-node',
                userId: 'stale-user',
                assigns: { role: 'user' },
                presence: { status: 'online' },
            });

            expect(channelEngine.users.has('stale-user')).toBe(true);

            jest.advanceTimersByTime(mockBackend.heartbeatTimeoutMs + 100);

            expect(channelEngine.users.has('stale-user')).toBe(false);
            expect(channelEngine.getPresence()['stale-user']).toBeUndefined();
        });

        it('should not clean up nodes with recent heartbeats', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            mockBackend.simulateMessage({
                type: DistributedMessageType.USER_JOINED,
                endpointName: 'String',
                channelName,
                sourceNodeId: 'active-node',
                userId: 'active-user',
                assigns: {},
                presence: {},
            });

            jest.advanceTimersByTime(200);
            mockBackend.simulateHeartbeat('active-node');
            jest.advanceTimersByTime(200);
            mockBackend.simulateHeartbeat('active-node');
            jest.advanceTimersByTime(200);

            expect(channelEngine.users.has('active-user')).toBe(true);
        });
    });

    describe('close with backend', () => {
        it('should clean up distributed subscriptions', () => {
            channelEngine.addUser('user1', {}, jest.fn());
            channelEngine.trackPresence('user1', { status: 'online' });

            expect(() => channelEngine.close()).not.toThrow();

            expect(channelEngine.users.size).toBe(0);
        });
    });

    describe('broadcast failure handling', () => {
        it('should silently handle broadcast errors', async () => {
            const failingBackend = new MockDistributedBackend();

            failingBackend.broadcast = jest.fn().mockRejectedValue(new Error('network error'));

            const engine = new ChannelEngine(mockLobbyEngine, 'fail-ch', failingBackend);

            engine.addUser('user1', {}, jest.fn());

            await Promise.resolve();

            expect(() => {
                engine.sendMessage(
                    'user1',
                    ChannelReceiver.ALL_USERS,
                    ServerActions.BROADCAST,
                    'test',
                    {},
                );
            }).not.toThrow();

            engine.close();
        });
    });

    describe('trackNodeUser / untrackNodeUser edge cases', () => {
        it('should handle undefined sourceNodeId gracefully', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            expect(() => {
                mockBackend.simulateMessage({
                    type: DistributedMessageType.USER_JOINED,
                    endpointName: 'String',
                    channelName,
                    userId: 'remote-user',
                    assigns: {},
                    presence: {},
                });
            }).not.toThrow();
        });

        it('should handle untrack for unknown sourceNodeId', () => {
            channelEngine.addUser('local-user', {}, jest.fn());

            expect(() => {
                mockBackend.simulateMessage({
                    type: DistributedMessageType.USER_LEFT,
                    endpointName: 'String',
                    channelName,
                    userId: 'unknown-user',
                });
            }).not.toThrow();
        });
    });
});
