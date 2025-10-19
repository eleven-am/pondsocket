import { ClientActions, PresenceEventTypes, ServerActions } from './enums';

// ============================================================================
// Types
// ============================================================================

export interface ClientMessage {
    event: string;
    requestId: string;
    channelName: string;
    payload: Record<string, any>;
    action: ClientActions;
}

export interface PresenceMessage {
    requestId: string;
    channelName: string;
    event: PresenceEventTypes;
    action: ServerActions.PRESENCE;
    payload: {
        presence: Array<Record<string, any>>;
        changed: Record<string, any>;
    };
}

export interface ServerMessage {
    event: string;
    requestId: string;
    channelName: string;
    payload: Record<string, any>;
    action: ServerActions.BROADCAST | ServerActions.CONNECT | ServerActions.ERROR | ServerActions.SYSTEM;
}

type ChannelEvent = ServerMessage | PresenceMessage;

// ============================================================================
// Validation Error
// ============================================================================

export class ValidationError extends Error {
    constructor (message: string, public readonly path?: string) {
        super(path ? `${path}: ${message}` : message);
        this.name = 'ValidationError';
    }
}

// ============================================================================
// Validation Utilities
// ============================================================================

function isObject (value: unknown): value is Record<string, any> {
    return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function isString (value: unknown): value is string {
    return typeof value === 'string';
}

function isArray (value: unknown): value is any[] {
    return Array.isArray(value);
}

function isRecord (value: unknown): value is Record<string, any> {
    if (!isObject(value)) {
        return false;
    }

    return Object.keys(value)
        .every((key) => typeof key === 'string');
}

function validateString (value: unknown, fieldName: string): asserts value is string {
    if (!isString(value)) {
        throw new ValidationError(`Expected string, got ${typeof value}`, fieldName);
    }
}

function validateObject (value: unknown, fieldName: string): asserts value is Record<string, any> {
    if (!isObject(value)) {
        throw new ValidationError(`Expected object, got ${typeof value}`, fieldName);
    }
}

function validateRecord (value: unknown, fieldName: string): asserts value is Record<string, any> {
    if (!isRecord(value)) {
        throw new ValidationError(`Expected record with string keys, got ${typeof value}`, fieldName);
    }
}

function validateArray (value: unknown, fieldName: string): asserts value is any[] {
    if (!isArray(value)) {
        throw new ValidationError(`Expected array, got ${typeof value}`, fieldName);
    }
}

function validateEnum<T extends string | number> (
    value: unknown,
    enumObj: Record<string, T>,
    fieldName: string,
): asserts value is T {
    const validValues = Object.values(enumObj);

    if (!validValues.includes(value as T)) {
        throw new ValidationError(
            `Expected one of [${validValues.join(', ')}], got ${JSON.stringify(value)}`,
            fieldName,
        );
    }
}

// ============================================================================
// Schema Validators
// ============================================================================

export const clientMessageSchema = {
    parse (data: unknown): ClientMessage {
        validateObject(data, 'clientMessage');

        const obj = data as Record<string, unknown>;

        // Validate required fields
        if (!('event' in obj)) {
            throw new ValidationError('Missing required field', 'event');
        }
        if (!('requestId' in obj)) {
            throw new ValidationError('Missing required field', 'requestId');
        }
        if (!('channelName' in obj)) {
            throw new ValidationError('Missing required field', 'channelName');
        }
        if (!('payload' in obj)) {
            throw new ValidationError('Missing required field', 'payload');
        }
        if (!('action' in obj)) {
            throw new ValidationError('Missing required field', 'action');
        }

        // Validate types
        validateString(obj.event, 'event');
        validateString(obj.requestId, 'requestId');
        validateString(obj.channelName, 'channelName');
        validateRecord(obj.payload, 'payload');
        validateEnum(obj.action, ClientActions, 'action');

        return {
            event: obj.event,
            requestId: obj.requestId,
            channelName: obj.channelName,
            payload: obj.payload,
            action: obj.action,
        };
    },
};

export const presenceMessageSchema = {
    parse (data: unknown): PresenceMessage {
        validateObject(data, 'presenceMessage');

        const obj = data as Record<string, unknown>;

        // Validate required fields
        if (!('requestId' in obj)) {
            throw new ValidationError('Missing required field', 'requestId');
        }
        if (!('channelName' in obj)) {
            throw new ValidationError('Missing required field', 'channelName');
        }
        if (!('event' in obj)) {
            throw new ValidationError('Missing required field', 'event');
        }
        if (!('action' in obj)) {
            throw new ValidationError('Missing required field', 'action');
        }
        if (!('payload' in obj)) {
            throw new ValidationError('Missing required field', 'payload');
        }

        // Validate types
        validateString(obj.requestId, 'requestId');
        validateString(obj.channelName, 'channelName');
        validateEnum(obj.event, PresenceEventTypes, 'event');

        // Validate action is exactly PRESENCE
        if (obj.action !== ServerActions.PRESENCE) {
            throw new ValidationError(
                `Expected ${ServerActions.PRESENCE}, got ${JSON.stringify(obj.action)}`,
                'action',
            );
        }

        // Validate payload structure
        validateObject(obj.payload, 'payload');

        const payload = obj.payload as Record<string, unknown>;

        if (!('presence' in payload)) {
            throw new ValidationError('Missing required field', 'payload.presence');
        }
        if (!('changed' in payload)) {
            throw new ValidationError('Missing required field', 'payload.changed');
        }

        validateArray(payload.presence, 'payload.presence');

        // Validate each presence item is a record
        (payload.presence as unknown[]).forEach((item, index) => {
            validateRecord(item, `payload.presence[${index}]`);
        });

        validateRecord(payload.changed, 'payload.changed');

        return {
            requestId: obj.requestId,
            channelName: obj.channelName,
            event: obj.event,
            action: ServerActions.PRESENCE,
            payload: {
                presence: payload.presence as Array<Record<string, any>>,
                changed: payload.changed as Record<string, any>,
            },
        };
    },
};

export const serverMessageSchema = {
    parse (data: unknown): ServerMessage {
        validateObject(data, 'serverMessage');

        const obj = data as Record<string, unknown>;

        // Validate required fields
        if (!('event' in obj)) {
            throw new ValidationError('Missing required field', 'event');
        }
        if (!('requestId' in obj)) {
            throw new ValidationError('Missing required field', 'requestId');
        }
        if (!('channelName' in obj)) {
            throw new ValidationError('Missing required field', 'channelName');
        }
        if (!('payload' in obj)) {
            throw new ValidationError('Missing required field', 'payload');
        }
        if (!('action' in obj)) {
            throw new ValidationError('Missing required field', 'action');
        }

        // Validate types
        validateString(obj.event, 'event');
        validateString(obj.requestId, 'requestId');
        validateString(obj.channelName, 'channelName');
        validateRecord(obj.payload, 'payload');

        // Validate action is one of the allowed server actions
        const validActions = [
            ServerActions.BROADCAST,
            ServerActions.CONNECT,
            ServerActions.ERROR,
            ServerActions.SYSTEM,
        ];

        if (!validActions.includes(obj.action as ServerActions)) {
            throw new ValidationError(
                `Expected one of [${validActions.join(', ')}], got ${JSON.stringify(obj.action)}`,
                'action',
            );
        }

        return {
            event: obj.event,
            requestId: obj.requestId,
            channelName: obj.channelName,
            payload: obj.payload,
            action: obj.action as ServerActions.BROADCAST | ServerActions.CONNECT | ServerActions.ERROR | ServerActions.SYSTEM,
        };
    },
};

export const channelEventSchema = {
    parse (data: unknown): ChannelEvent {
        validateObject(data, 'channelEvent');

        const obj = data as Record<string, unknown>;

        // Check action to determine which schema to use
        if (!('action' in obj)) {
            throw new ValidationError('Missing required field', 'action');
        }

        // Discriminate based on action field
        if (obj.action === ServerActions.PRESENCE) {
            return presenceMessageSchema.parse(data);
        }

        return serverMessageSchema.parse(data);
    },
};
