import { PondPresence, PresenceEventTypes, ServerActions } from '@eleven-am/pondsocket-common';

import { PresenceEngine } from '../engines/presenceEngine';

describe('PresenceEngine', () => {
    let presenceEngine: PresenceEngine;
    const channelId = 'test-channel';

    beforeEach(() => {
        presenceEngine = new PresenceEngine(channelId);
    });

    describe('constructor', () => {
        it('should initialize with empty presence cache', () => {
            expect(presenceEngine.presenceCount).toBe(0);
            expect(presenceEngine.getAllPresence().size).toBe(0);
        });
    });

    describe('trackPresence', () => {
        it('should add a new user presence', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);

            expect(presenceEngine.presenceCount).toBe(1);
            expect(presenceEngine.getPresence(userId)).toEqual(presence);
        });

        it('should throw an error if user already exists', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);

            expect(() => {
                presenceEngine.trackPresence(userId, presence);
            }).toThrow(`User with id ${userId} already exists in the presence cache`);
        });

        it('should throw an error if presence data is null', () => {
            const userId = 'user1';

            expect(() => {
                presenceEngine.trackPresence(userId, null as unknown as PondPresence);
            }).toThrow(`Data is required for ${PresenceEventTypes.JOIN} action`);
        });

        it('should publish a presence event', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };
            const callback = jest.fn();

            presenceEngine.subscribe(callback);
            presenceEngine.trackPresence(userId, presence);

            expect(callback).toHaveBeenCalledWith(expect.objectContaining({
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                channelName: channelId,
                payload: {
                    changed: presence,
                    presence: [presence],
                },
                recipients: [userId],
            }));
        });
    });

    describe('updatePresence', () => {
        it('should update an existing user presence', () => {
            const userId = 'user1';
            const initialPresence: PondPresence = { status: 'online' };
            const updatedPresence: PondPresence = { status: 'away' };

            presenceEngine.trackPresence(userId, initialPresence);
            presenceEngine.updatePresence(userId, updatedPresence);

            expect(presenceEngine.presenceCount).toBe(1);
            expect(presenceEngine.getPresence(userId)).toEqual(updatedPresence);
        });

        it('should throw an error if user does not exist', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            expect(() => {
                presenceEngine.updatePresence(userId, presence);
            }).toThrow(`User with id ${userId} does not exist in the presence cache`);
        });

        it('should publish a presence event with previous and new data', () => {
            const userId = 'user1';
            const initialPresence: PondPresence = { status: 'online' };
            const updatedPresence: PondPresence = { status: 'away' };
            const callback = jest.fn();

            presenceEngine.trackPresence(userId, initialPresence);
            presenceEngine.subscribe(callback);
            presenceEngine.updatePresence(userId, updatedPresence);

            expect(callback).toHaveBeenCalledWith(expect.objectContaining({
                event: PresenceEventTypes.UPDATE,
                action: ServerActions.PRESENCE,
                channelName: channelId,
                payload: {
                    changed: initialPresence,
                    presence: [updatedPresence],
                },
                recipients: [userId],
            }));
        });
    });

    describe('removePresence', () => {
        it('should remove a user presence', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);
            presenceEngine.removePresence(userId);

            expect(presenceEngine.presenceCount).toBe(0);
            expect(presenceEngine.getPresence(userId)).toBeNull();
        });

        it('should throw an error if user does not exist', () => {
            const userId = 'user1';

            expect(() => {
                presenceEngine.removePresence(userId);
            }).toThrow(`User with id ${userId} does not exist in the presence cache`);
        });

        it('should publish a presence event with the removed user data', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };
            const callback = jest.fn();

            presenceEngine.trackPresence(userId, presence);
            presenceEngine.subscribe(callback);
            presenceEngine.removePresence(userId);

            expect(callback).toHaveBeenCalledWith(expect.objectContaining({
                event: PresenceEventTypes.LEAVE,
                action: ServerActions.PRESENCE,
                channelName: channelId,
                payload: {
                    changed: presence,
                    presence: [],
                },
                recipients: [],
            }));
        });
    });

    describe('upsertPresence', () => {
        it('should add presence if user does not exist', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.upsertPresence(userId, presence);

            expect(presenceEngine.presenceCount).toBe(1);
            expect(presenceEngine.getPresence(userId)).toEqual(presence);
        });

        it('should update presence if user exists', () => {
            const userId = 'user1';
            const initialPresence: PondPresence = { status: 'online' };
            const updatedPresence: PondPresence = { status: 'away' };

            presenceEngine.trackPresence(userId, initialPresence);
            presenceEngine.upsertPresence(userId, updatedPresence);

            expect(presenceEngine.presenceCount).toBe(1);
            expect(presenceEngine.getPresence(userId)).toEqual(updatedPresence);
        });
    });

    describe('getPresence', () => {
        it('should return null for non-existent user', () => {
            expect(presenceEngine.getPresence('nonexistent')).toBeNull();
        });

        it('should return presence data for existing user', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);

            expect(presenceEngine.getPresence(userId)).toEqual(presence);
        });
    });

    describe('getAllPresence', () => {
        it('should return empty map when no presences exist', () => {
            expect(presenceEngine.getAllPresence().size).toBe(0);
        });

        it('should return map with all presences', () => {
            const user1 = 'user1';
            const user2 = 'user2';
            const presence1: PondPresence = { status: 'online' };
            const presence2: PondPresence = { status: 'away' };

            presenceEngine.trackPresence(user1, presence1);
            presenceEngine.trackPresence(user2, presence2);

            const allPresence = presenceEngine.getAllPresence();

            expect(allPresence.size).toBe(2);
            expect(allPresence.get(user1)).toEqual(presence1);
            expect(allPresence.get(user2)).toEqual(presence2);
        });

        it('should return a copy of the presence cache', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);

            const allPresence = presenceEngine.getAllPresence();

            allPresence.delete(userId);
            expect(presenceEngine.presenceCount).toBe(1);
            expect(presenceEngine.getPresence(userId)).toEqual(presence);
        });
    });

    describe('subscribe', () => {
        it('should allow subscription to presence events', () => {
            const callback = jest.fn();
            const unsubscribe = presenceEngine.subscribe(callback);

            expect(typeof unsubscribe).toBe('function');
        });

        it('should call subscriber when presence changes', () => {
            const callback = jest.fn();

            presenceEngine.subscribe(callback);

            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);

            expect(callback).toHaveBeenCalledTimes(1);
        });

        it('should support multiple subscribers', () => {
            const callback1 = jest.fn();
            const callback2 = jest.fn();

            presenceEngine.subscribe(callback1);
            presenceEngine.subscribe(callback2);

            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);

            expect(callback1).toHaveBeenCalledTimes(1);
            expect(callback2).toHaveBeenCalledTimes(1);
        });

        it('should stop calling subscriber after unsubscribe', () => {
            const callback = jest.fn();
            const unsubscribe = presenceEngine.subscribe(callback);

            unsubscribe();

            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);

            expect(callback).not.toHaveBeenCalled();
        });
    });

    describe('close', () => {
        it('should clear all presence data', () => {
            const userId = 'user1';
            const presence: PondPresence = { status: 'online' };

            presenceEngine.trackPresence(userId, presence);
            presenceEngine.close();

            expect(presenceEngine.presenceCount).toBe(0);
            expect(presenceEngine.getPresence(userId)).toBeNull();
        });

        it('should stop calling subscribers after close', () => {
            const callback = jest.fn();

            presenceEngine.subscribe(callback);

            presenceEngine.close();

            // Try to add presence after close - subscribers should not be called
            try {
                const userId = 'user1';
                const presence: PondPresence = { status: 'online' };

                presenceEngine.trackPresence(userId, presence);
            } catch (e) {
                // May throw error due to closed publisher, that's okay
            }

            expect(callback).not.toHaveBeenCalled();
        });
    });

    describe('multiple user scenarios', () => {
        it('should handle multiple users correctly', () => {
            const user1 = 'user1';
            const user2 = 'user2';
            const user3 = 'user3';

            const presence1: PondPresence = { status: 'online' };
            const presence2: PondPresence = { status: 'away' };
            const presence3: PondPresence = { status: 'offline' };

            presenceEngine.trackPresence(user1, presence1);
            presenceEngine.trackPresence(user2, presence2);
            presenceEngine.trackPresence(user3, presence3);

            expect(presenceEngine.presenceCount).toBe(3);

            presenceEngine.removePresence(user2);

            expect(presenceEngine.presenceCount).toBe(2);
            expect(presenceEngine.getPresence(user1)).toEqual(presence1);
            expect(presenceEngine.getPresence(user2)).toBeNull();
            expect(presenceEngine.getPresence(user3)).toEqual(presence3);

            const updatedPresence: PondPresence = { status: 'busy' };

            presenceEngine.updatePresence(user3, updatedPresence);

            expect(presenceEngine.getPresence(user3)).toEqual(updatedPresence);
        });

        it('should emit events with correct recipients', () => {
            const callback = jest.fn();

            presenceEngine.subscribe(callback);

            const user1 = 'user1';
            const user2 = 'user2';

            const presence1: PondPresence = { status: 'online' };
            const presence2: PondPresence = { status: 'away' };

            presenceEngine.trackPresence(user1, presence1);

            // First event should have only user1 as recipient
            expect(callback.mock.calls[0][0].recipients).toEqual([user1]);

            presenceEngine.trackPresence(user2, presence2);

            // Second event should have both users as recipients
            expect(callback.mock.calls[1][0].recipients).toEqual([user1, user2]);

            presenceEngine.removePresence(user1);

            // Third event should have only user2 as recipient
            expect(callback.mock.calls[2][0].recipients).toEqual([user2]);
        });
    });
});
