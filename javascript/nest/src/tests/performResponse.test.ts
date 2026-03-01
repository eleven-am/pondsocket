import { performResponse } from '../performers/response';

function createJoinContext() {
    return {
        accept: jest.fn(),
        decline: jest.fn(),
        assign: jest.fn().mockReturnThis(),
        reply: jest.fn(),
        broadcast: jest.fn(),
        broadcastFrom: jest.fn(),
        broadcastTo: jest.fn(),
        hasResponded: false,
        user: { id: 'u1', assigns: {}, presence: {} },
        channel: { upsertPresence: jest.fn() },
        event: { params: {}, query: {}, payload: {}, event: 'JOIN' },
    };
}

function createEventContext() {
    return {
        assign: jest.fn().mockReturnThis(),
        reply: jest.fn(),
        broadcast: jest.fn(),
        broadcastFrom: jest.fn(),
        broadcastTo: jest.fn(),
        user: { id: 'u1', assigns: {}, presence: {} },
        channel: { upsertPresence: jest.fn() },
        event: { params: {}, query: {}, payload: {}, event: 'test_event' },
    };
}

function createConnectionContext() {
    return {
        accept: jest.fn(),
        decline: jest.fn(),
        assign: jest.fn().mockReturnThis(),
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
        channel: { upsertPresence: jest.fn() },
    };
}

describe('performResponse', () => {
    describe('join context', () => {
        it('calls accept and sets assigns', () => {
            const ctx = createJoinContext();
            const data = { assigns: { role: 'admin' } };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.assign).toHaveBeenCalledWith({ role: 'admin' });
            expect(ctx.accept).toHaveBeenCalled();
        });

        it('calls reply with event and payload', () => {
            const ctx = createJoinContext();
            const data = { event: 'welcome', message: 'hello' };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.reply).toHaveBeenCalledWith('welcome', { message: 'hello' });
        });

        it('calls broadcast', () => {
            const ctx = createJoinContext();
            const data = { broadcast: 'user_joined', name: 'alice' };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.broadcast).toHaveBeenCalledWith('user_joined', { name: 'alice' });
        });

        it('calls broadcastFrom', () => {
            const ctx = createJoinContext();
            const data = { broadcastFrom: 'user_action', detail: 'moved' };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.broadcastFrom).toHaveBeenCalledWith('user_action', { detail: 'moved' });
        });

        it('calls broadcastTo', () => {
            const ctx = createJoinContext();
            const data = { broadcastTo: { event: 'dm', users: ['u2', 'u3'] }, text: 'hi' };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.broadcastTo).toHaveBeenCalledWith('dm', { text: 'hi' }, ['u2', 'u3']);
        });
    });

    describe('event context', () => {
        it('calls reply with event and payload', () => {
            const ctx = createEventContext();
            const data = { event: 'response_event', value: 42 };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.reply).toHaveBeenCalledWith('response_event', { value: 42 });
        });

        it('calls assign but not accept', () => {
            const ctx = createEventContext();
            const data = { assigns: { score: 10 } };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.assign).toHaveBeenCalledWith({ score: 10 });
            expect((ctx as any).accept).toBeUndefined();
        });

        it('calls broadcast, broadcastFrom, broadcastTo', () => {
            const ctx = createEventContext();
            const data = {
                broadcast: 'bc_event',
                broadcastFrom: 'bf_event',
                broadcastTo: { event: 'bt_event', users: ['u2'] },
                info: 'test',
            };

            performResponse('u1', ctx.channel as any, data, ctx as any);

            expect(ctx.broadcast).toHaveBeenCalledWith('bc_event', { info: 'test' });
            expect(ctx.broadcastFrom).toHaveBeenCalledWith('bf_event', { info: 'test' });
            expect(ctx.broadcastTo).toHaveBeenCalledWith('bt_event', { info: 'test' }, ['u2']);
        });
    });

    describe('presence tracking', () => {
        it('calls upsertPresence when presence data is provided with a channel', () => {
            const ctx = createJoinContext();
            const channel = ctx.channel;
            const data = { presence: { status: 'active' } };

            performResponse('u1', channel as any, data, ctx as any);

            expect(channel.upsertPresence).toHaveBeenCalledWith('u1', { status: 'active' });
        });

        it('does not call upsertPresence when channel is null', () => {
            const ctx = createEventContext();
            const data = { presence: { status: 'active' } };

            performResponse('u1', null, data, ctx as any);
        });
    });

    describe('null/undefined response is a no-op', () => {
        it('does nothing when data is null', () => {
            const ctx = createJoinContext();

            performResponse('u1', ctx.channel as any, null, ctx as any);

            expect(ctx.accept).not.toHaveBeenCalled();
            expect(ctx.reply).not.toHaveBeenCalled();
        });

        it('does nothing when data is undefined', () => {
            const ctx = createJoinContext();

            performResponse('u1', ctx.channel as any, undefined, ctx as any);

            expect(ctx.accept).not.toHaveBeenCalled();
            expect(ctx.reply).not.toHaveBeenCalled();
        });
    });

    describe('leave event', () => {
        it('returns early when data is null', () => {
            const ctx = createLeaveEvent();

            performResponse('u1', ctx.channel as any, null, ctx as any);
        });

        it('returns early when data is undefined', () => {
            const ctx = createLeaveEvent();

            performResponse('u1', ctx.channel as any, undefined, ctx as any);
        });
    });

    describe('connection context', () => {
        it('calls accept and sets assigns', () => {
            const ctx = createConnectionContext();
            const data = { assigns: { token: 'abc' } };

            performResponse('c1', null, data, ctx as any);

            expect(ctx.assign).toHaveBeenCalledWith({ token: 'abc' });
            expect(ctx.accept).toHaveBeenCalled();
        });

        it('does not process if hasResponded is true', () => {
            const ctx = createConnectionContext();
            ctx.hasResponded = true;
            const data = { assigns: { token: 'abc' } };

            performResponse('c1', null, data, ctx as any);

            expect(ctx.assign).not.toHaveBeenCalled();
            expect(ctx.accept).not.toHaveBeenCalled();
        });
    });
});
