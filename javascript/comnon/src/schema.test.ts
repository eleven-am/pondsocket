import { ClientActions, ServerActions, PresenceEventTypes } from './enums';
import {
    clientMessageSchema,
    serverMessageSchema,
    presenceMessageSchema,
    channelEventSchema,
    ValidationError,
} from './schema';

describe('Schema Validation', () => {
    describe('clientMessageSchema', () => {
        it('should validate a valid client message', () => {
            const validMessage = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: { data: 'test' },
                action: ClientActions.BROADCAST,
            };

            const result = clientMessageSchema.parse(validMessage);

            expect(result).toEqual(validMessage);
        });

        it('should throw ValidationError for missing event field', () => {
            const invalidMessage = {
                requestId: '123',
                channelName: 'channel',
                payload: {},
                action: ClientActions.BROADCAST,
            };

            expect(() => clientMessageSchema.parse(invalidMessage)).toThrow(ValidationError);
            expect(() => clientMessageSchema.parse(invalidMessage)).toThrow('event: Missing required field');
        });

        it('should throw ValidationError for invalid action enum', () => {
            const invalidMessage = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: {},
                action: 'INVALID_ACTION',
            };

            expect(() => clientMessageSchema.parse(invalidMessage)).toThrow(ValidationError);
            expect(() => clientMessageSchema.parse(invalidMessage)).toThrow(/action: Expected one of/);
        });

        it('should throw ValidationError for non-object payload', () => {
            const invalidMessage = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: 'not an object',
                action: ClientActions.BROADCAST,
            };

            expect(() => clientMessageSchema.parse(invalidMessage)).toThrow(ValidationError);
            expect(() => clientMessageSchema.parse(invalidMessage)).toThrow(/payload:/);
        });
    });

    describe('serverMessageSchema', () => {
        it('should validate a valid server message', () => {
            const validMessage = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: { data: 'test' },
                action: ServerActions.BROADCAST,
            };

            const result = serverMessageSchema.parse(validMessage);

            expect(result).toEqual(validMessage);
        });

        it('should accept all valid server actions', () => {
            const validActions = [
                ServerActions.BROADCAST,
                ServerActions.CONNECT,
                ServerActions.ERROR,
                ServerActions.SYSTEM,
            ];

            validActions.forEach((action) => {
                const message = {
                    event: 'test',
                    requestId: '123',
                    channelName: 'channel',
                    payload: {},
                    action,
                };

                expect(() => serverMessageSchema.parse(message)).not.toThrow();
            });
        });

        it('should throw ValidationError for invalid action', () => {
            const invalidMessage = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: {},
                action: ServerActions.PRESENCE,
            };

            expect(() => serverMessageSchema.parse(invalidMessage)).toThrow(ValidationError);
        });
    });

    describe('presenceMessageSchema', () => {
        it('should validate a valid presence message', () => {
            const validMessage = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: {
                    presence: [{ userId: '1' }, { userId: '2' }],
                    changed: { userId: '1' },
                },
            };

            const result = presenceMessageSchema.parse(validMessage);

            expect(result).toEqual(validMessage);
        });

        it('should throw ValidationError when action is not PRESENCE', () => {
            const invalidMessage = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.BROADCAST,
                payload: {
                    presence: [],
                    changed: {},
                },
            };

            expect(() => presenceMessageSchema.parse(invalidMessage)).toThrow(ValidationError);
            expect(() => presenceMessageSchema.parse(invalidMessage)).toThrow(/action: Expected/);
        });

        it('should throw ValidationError for missing payload.presence', () => {
            const invalidMessage = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: {
                    changed: {},
                },
            };

            expect(() => presenceMessageSchema.parse(invalidMessage)).toThrow(ValidationError);
            expect(() => presenceMessageSchema.parse(invalidMessage)).toThrow(/payload.presence/);
        });

        it('should throw ValidationError when presence array contains non-objects', () => {
            const invalidMessage = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: {
                    presence: ['not an object'],
                    changed: {},
                },
            };

            expect(() => presenceMessageSchema.parse(invalidMessage)).toThrow(ValidationError);
        });
    });

    describe('channelEventSchema', () => {
        it('should parse server message when action is not PRESENCE', () => {
            const serverMessage = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: {},
                action: ServerActions.BROADCAST,
            };

            const result = channelEventSchema.parse(serverMessage);

            expect(result).toEqual(serverMessage);
        });

        it('should parse presence message when action is PRESENCE', () => {
            const presenceMessage = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: {
                    presence: [],
                    changed: {},
                },
            };

            const result = channelEventSchema.parse(presenceMessage);

            expect(result).toEqual(presenceMessage);
        });

        it('should throw ValidationError when action is missing', () => {
            const invalidMessage = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: {},
            };

            expect(() => channelEventSchema.parse(invalidMessage)).toThrow(ValidationError);
            expect(() => channelEventSchema.parse(invalidMessage)).toThrow(/action: Missing required field/);
        });
    });

    describe('clientMessageSchema - missing fields', () => {
        it('should throw for missing requestId', () => {
            const msg = {
                event: 'test',
                channelName: 'channel',
                payload: {},
                action: ClientActions.BROADCAST,
            };

            expect(() => clientMessageSchema.parse(msg)).toThrow('requestId: Missing required field');
        });

        it('should throw for missing channelName', () => {
            const msg = {
                event: 'test',
                requestId: '123',
                payload: {},
                action: ClientActions.BROADCAST,
            };

            expect(() => clientMessageSchema.parse(msg)).toThrow('channelName: Missing required field');
        });

        it('should throw for missing payload', () => {
            const msg = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                action: ClientActions.BROADCAST,
            };

            expect(() => clientMessageSchema.parse(msg)).toThrow('payload: Missing required field');
        });

        it('should throw for missing action', () => {
            const msg = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: {},
            };

            expect(() => clientMessageSchema.parse(msg)).toThrow('action: Missing required field');
        });

        it('should throw for non-string event', () => {
            const msg = {
                event: 123,
                requestId: '123',
                channelName: 'channel',
                payload: {},
                action: ClientActions.BROADCAST,
            };

            expect(() => clientMessageSchema.parse(msg)).toThrow('event: Expected string');
        });

        it('should throw when data is not an object', () => {
            expect(() => clientMessageSchema.parse('not an object')).toThrow('clientMessage: Expected object');
            expect(() => clientMessageSchema.parse(null)).toThrow('clientMessage: Expected object');
            expect(() => clientMessageSchema.parse([])).toThrow('clientMessage: Expected object');
        });
    });

    describe('serverMessageSchema - missing fields', () => {
        it('should throw for missing event', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                payload: {},
                action: ServerActions.BROADCAST,
            };

            expect(() => serverMessageSchema.parse(msg)).toThrow('event: Missing required field');
        });

        it('should throw for missing requestId', () => {
            const msg = {
                event: 'test',
                channelName: 'channel',
                payload: {},
                action: ServerActions.BROADCAST,
            };

            expect(() => serverMessageSchema.parse(msg)).toThrow('requestId: Missing required field');
        });

        it('should throw for missing channelName', () => {
            const msg = {
                event: 'test',
                requestId: '123',
                payload: {},
                action: ServerActions.BROADCAST,
            };

            expect(() => serverMessageSchema.parse(msg)).toThrow('channelName: Missing required field');
        });

        it('should throw for missing payload', () => {
            const msg = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                action: ServerActions.BROADCAST,
            };

            expect(() => serverMessageSchema.parse(msg)).toThrow('payload: Missing required field');
        });

        it('should throw for missing action', () => {
            const msg = {
                event: 'test',
                requestId: '123',
                channelName: 'channel',
                payload: {},
            };

            expect(() => serverMessageSchema.parse(msg)).toThrow('action: Missing required field');
        });
    });

    describe('presenceMessageSchema - missing fields', () => {
        it('should throw for missing requestId', () => {
            const msg = {
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: { presence: [], changed: {} },
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('requestId: Missing required field');
        });

        it('should throw for missing channelName', () => {
            const msg = {
                requestId: '123',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: { presence: [], changed: {} },
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('channelName: Missing required field');
        });

        it('should throw for missing event', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                action: ServerActions.PRESENCE,
                payload: { presence: [], changed: {} },
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('event: Missing required field');
        });

        it('should throw for missing action', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                payload: { presence: [], changed: {} },
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('action: Missing required field');
        });

        it('should throw for missing payload', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('payload: Missing required field');
        });

        it('should throw for missing payload.changed', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: { presence: [] },
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('payload.changed: Missing required field');
        });

        it('should throw for non-object payload', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: 'not an object',
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('payload: Expected object');
        });

        it('should throw for non-array presence', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: { presence: 'not an array', changed: {} },
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('payload.presence: Expected array');
        });

        it('should throw for non-record changed', () => {
            const msg = {
                requestId: '123',
                channelName: 'channel',
                event: PresenceEventTypes.JOIN,
                action: ServerActions.PRESENCE,
                payload: { presence: [], changed: 'not an object' },
            };

            expect(() => presenceMessageSchema.parse(msg)).toThrow('payload.changed: Expected record');
        });
    });

    describe('ValidationError', () => {
        it('should include path in error message', () => {
            const error = new ValidationError('Test error', 'field.path');

            expect(error.message).toBe('field.path: Test error');
            expect(error.path).toBe('field.path');
            expect(error.name).toBe('ValidationError');
        });

        it('should work without path', () => {
            const error = new ValidationError('Test error');

            expect(error.message).toBe('Test error');
            expect(error.path).toBeUndefined();
        });
    });
});
