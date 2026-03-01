import { isEventContext, isJoinContext, isConnectionContext, isLeaveEvent } from '../performers/narrow';

function createJoinContext() {
    return {
        accept: jest.fn(),
        decline: jest.fn(),
        assign: jest.fn(),
        reply: jest.fn(),
        broadcast: jest.fn(),
        broadcastFrom: jest.fn(),
        broadcastTo: jest.fn(),
        hasResponded: false,
        user: { id: 'u1', assigns: {}, presence: {} },
        channel: {},
        event: { params: {}, query: {}, payload: {}, event: 'JOIN' },
    };
}

function createEventContext() {
    return {
        assign: jest.fn(),
        reply: jest.fn(),
        broadcast: jest.fn(),
        broadcastFrom: jest.fn(),
        broadcastTo: jest.fn(),
        user: { id: 'u1', assigns: {}, presence: {} },
        channel: {},
        event: { params: {}, query: {}, payload: {}, event: 'test_event' },
    };
}

function createConnectionContext() {
    return {
        accept: jest.fn(),
        decline: jest.fn(),
        assign: jest.fn(),
        reply: jest.fn(),
        hasResponded: false,
        clientId: 'c1',
        params: {},
        query: {},
    };
}

function createLeaveEvent() {
    return {
        user: { id: 'u1', assigns: {}, presence: {} },
        channel: {},
    };
}

describe('narrow', () => {
    describe('isJoinContext', () => {
        it('returns true for a join context', () => {
            expect(isJoinContext(createJoinContext() as any)).toBe(true);
        });

        it('returns false for an event context', () => {
            expect(isJoinContext(createEventContext() as any)).toBe(false);
        });

        it('returns false for a connection context', () => {
            expect(isJoinContext(createConnectionContext() as any)).toBe(false);
        });

        it('returns false for a leave event', () => {
            expect(isJoinContext(createLeaveEvent() as any)).toBe(false);
        });
    });

    describe('isEventContext', () => {
        it('returns true for an event context', () => {
            expect(isEventContext(createEventContext() as any)).toBe(true);
        });

        it('returns false for a join context', () => {
            expect(isEventContext(createJoinContext() as any)).toBe(false);
        });

        it('returns false for a connection context', () => {
            expect(isEventContext(createConnectionContext() as any)).toBe(false);
        });

        it('returns true for a leave event (no accept property)', () => {
            expect(isEventContext(createLeaveEvent() as any)).toBe(true);
        });
    });

    describe('isConnectionContext', () => {
        it('returns true for a connection context', () => {
            expect(isConnectionContext(createConnectionContext() as any)).toBe(true);
        });

        it('returns false for a join context', () => {
            expect(isConnectionContext(createJoinContext() as any)).toBe(false);
        });

        it('returns false for an event context', () => {
            expect(isConnectionContext(createEventContext() as any)).toBe(false);
        });

        it('returns false for a leave event', () => {
            expect(isConnectionContext(createLeaveEvent() as any)).toBe(false);
        });
    });

    describe('isLeaveEvent', () => {
        it('returns false for a leave event due to isEventContext overlap', () => {
            expect(isLeaveEvent(createLeaveEvent() as any)).toBe(false);
        });

        it('returns false for a join context', () => {
            expect(isLeaveEvent(createJoinContext() as any)).toBe(false);
        });

        it('returns false for an event context', () => {
            expect(isLeaveEvent(createEventContext() as any)).toBe(false);
        });

        it('returns false for a connection context', () => {
            expect(isLeaveEvent(createConnectionContext() as any)).toBe(false);
        });
    });
});
