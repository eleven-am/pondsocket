import { Context } from '../context/context';
import { NestContext } from '../types';

function createConnectionNestContext(): NestContext {
    return {
        connection: {
            accept: jest.fn(),
            decline: jest.fn(),
            assign: jest.fn(),
            reply: jest.fn(),
            hasResponded: false,
            clientId: 'client-123',
            params: { id: '42' },
            query: { token: 'abc' },
        } as any,
    };
}

function createJoinNestContext(): NestContext {
    return {
        join: {
            accept: jest.fn(),
            decline: jest.fn(),
            assign: jest.fn(),
            reply: jest.fn(),
            broadcast: jest.fn(),
            broadcastFrom: jest.fn(),
            broadcastTo: jest.fn(),
            hasResponded: false,
            user: { id: 'user-1', assigns: { role: 'admin' }, presence: { status: 'online' } },
            channel: { upsertPresence: jest.fn(), name: 'room-1' },
            event: { params: { room: 'lobby' }, query: {}, payload: { msg: 'hi' }, event: 'join_room' },
        } as any,
    };
}

function createEventNestContext(): NestContext {
    return {
        event: {
            assign: jest.fn(),
            reply: jest.fn(),
            broadcast: jest.fn(),
            broadcastFrom: jest.fn(),
            broadcastTo: jest.fn(),
            user: { id: 'user-2', assigns: { score: 10 }, presence: { game: 'chess' } },
            channel: { upsertPresence: jest.fn(), name: 'game-room' },
            event: { params: { action: 'move' }, query: { piece: 'knight' }, payload: { x: 3, y: 5 }, event: 'make_move' },
        } as any,
    };
}

function createLeaveNestContext(): NestContext {
    return {
        leave: {
            user: { id: 'user-3', assigns: { name: 'bob' }, presence: { status: 'offline' } },
            channel: { upsertPresence: jest.fn(), name: 'chat-room' },
        } as any,
    };
}

class TestInstance {
    handler() {}
}

describe('Context', () => {
    describe('connection lifecycle', () => {
        it('returns connectionContext', () => {
            const nestCtx = createConnectionNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.connectionContext).toBe(nestCtx.connection);
            expect(ctx.joinContext).toBeNull();
            expect(ctx.eventContext).toBeNull();
            expect(ctx.leaveEvent).toBeNull();
        });

        it('returns user with clientId as id and empty assigns/presence', () => {
            const nestCtx = createConnectionNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.user.id).toBe('client-123');
            expect(ctx.user.assigns).toEqual({});
            expect(ctx.user.presence).toEqual({});
        });

        it('returns event with connection params and query', () => {
            const nestCtx = createConnectionNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.event).toEqual({
                params: { id: '42' },
                query: { token: 'abc' },
                payload: {},
                event: 'CONNECTION',
            });
        });

        it('returns null for channel', () => {
            const nestCtx = createConnectionNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.channel).toBeNull();
        });
    });

    describe('join lifecycle', () => {
        it('returns joinContext', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.joinContext).toBe(nestCtx.join);
            expect(ctx.connectionContext).toBeNull();
            expect(ctx.eventContext).toBeNull();
            expect(ctx.leaveEvent).toBeNull();
        });

        it('returns user from join context', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.user.id).toBe('user-1');
            expect(ctx.user.assigns).toEqual({ role: 'admin' });
        });

        it('returns presence and assigns', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.presence).toEqual({ status: 'online' });
            expect(ctx.assigns).toEqual({ role: 'admin' });
        });

        it('returns channel from join context', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.channel).toBe(nestCtx.join!.channel);
        });

        it('returns event from join context', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.event).toBe(nestCtx.join!.event);
        });
    });

    describe('event lifecycle', () => {
        it('returns eventContext', () => {
            const nestCtx = createEventNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.eventContext).toBe(nestCtx.event);
            expect(ctx.joinContext).toBeNull();
            expect(ctx.connectionContext).toBeNull();
            expect(ctx.leaveEvent).toBeNull();
        });

        it('returns user from event context', () => {
            const nestCtx = createEventNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.user.id).toBe('user-2');
            expect(ctx.user.assigns).toEqual({ score: 10 });
        });

        it('returns channel from event context', () => {
            const nestCtx = createEventNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.channel).toBe(nestCtx.event!.channel);
        });

        it('returns event from event context', () => {
            const nestCtx = createEventNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.event).toBe(nestCtx.event!.event);
        });
    });

    describe('leave lifecycle', () => {
        it('returns leaveEvent', () => {
            const nestCtx = createLeaveNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.leaveEvent).toBe(nestCtx.leave);
            expect(ctx.joinContext).toBeNull();
            expect(ctx.eventContext).toBeNull();
            expect(ctx.connectionContext).toBeNull();
        });

        it('returns user from leave event', () => {
            const nestCtx = createLeaveNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.user.id).toBe('user-3');
            expect(ctx.user.assigns).toEqual({ name: 'bob' });
        });

        it('returns channel from leave event', () => {
            const nestCtx = createLeaveNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.channel).toBe(nestCtx.leave!.channel);
        });

        it('returns null for event in leave lifecycle', () => {
            const nestCtx = createLeaveNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.event).toBeNull();
        });
    });

    describe('instance and method accessors', () => {
        it('getClass returns the constructor', () => {
            const nestCtx = createJoinNestContext();
            const instance = new TestInstance();
            const ctx = new Context(nestCtx, instance, 'handler');

            expect(ctx.getClass()).toBe(TestInstance);
        });

        it('getHandler returns the method function', () => {
            const nestCtx = createJoinNestContext();
            const instance = new TestInstance();
            const ctx = new Context(nestCtx, instance, 'handler');

            expect(ctx.getHandler()).toBe(instance.handler);
        });

        it('getInstance returns the instance', () => {
            const nestCtx = createJoinNestContext();
            const instance = new TestInstance();
            const ctx = new Context(nestCtx, instance, 'handler');

            expect(ctx.getInstance()).toBe(instance);
        });

        it('getMethod returns the property key', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.getMethod()).toBe('handler');
        });
    });

    describe('addData and getData', () => {
        it('stores and retrieves data', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            ctx.addData('myKey', { foo: 'bar' });
            expect(ctx.getData('myKey')).toEqual({ foo: 'bar' });
        });

        it('returns null for missing keys', () => {
            const nestCtx = createJoinNestContext();
            const ctx = new Context(nestCtx, new TestInstance(), 'handler');

            expect(ctx.getData('nonexistent')).toBeNull();
        });
    });
});
