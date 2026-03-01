import { Context } from '../context/context';
import { createContextParamDecorator } from '../helpers/createContextParamDecorator';

jest.mock('../helpers/createParamDecorator', () => ({
    createParamDecorator: (callback: any) => callback,
}));

function makeContext(overrides: Partial<Record<string, any>> = {}): Context {
    const nestCtx: any = {
        join: {
            user: { id: 'u1', assigns: { role: 'admin' }, presence: { status: 'online' } },
            channel: { upsertPresence: jest.fn() },
            event: { params: {}, query: {}, payload: {}, event: 'JOIN' },
            accept: jest.fn(),
            decline: jest.fn(),
            assign: jest.fn(),
            reply: jest.fn(),
            broadcast: jest.fn(),
            broadcastFrom: jest.fn(),
            broadcastTo: jest.fn(),
            hasResponded: false,
        },
    };
    const ctx = new Context(nestCtx, {}, 'handler');

    for (const [key, value] of Object.entries(overrides)) {
        Object.defineProperty(ctx, key, { get: () => value, configurable: true });
    }

    return ctx;
}

describe('createContextParamDecorator', () => {
    it('returns the value when the context key exists', () => {
        const callback = createContextParamDecorator('joinContext', 'GetJoinContext') as any;
        const context = makeContext();

        const result = callback(undefined, context, undefined);
        expect(result).not.toBeNull();
        expect(result).toBe(context.joinContext);
    });

    it('calls transform function when provided', () => {
        const transform = jest.fn((val: any) => val.user.id);
        const callback = createContextParamDecorator('joinContext', 'GetJoinContext', transform as any) as any;
        const context = makeContext();

        const result = callback(undefined, context, undefined);
        expect(transform).toHaveBeenCalledWith(context.joinContext);
        expect(result).toBe('u1');
    });

    it('throws when context key is null', () => {
        const callback = createContextParamDecorator('eventContext', 'GetEventContext') as any;
        const context = makeContext({ eventContext: null });

        expect(() => callback(undefined, context, undefined)).toThrow(
            'Invalid decorator usage: GetEventContext',
        );
    });

    it('throws when context key is undefined', () => {
        const callback = createContextParamDecorator('eventContext', 'GetEventContext') as any;
        const context = makeContext({ eventContext: undefined });

        expect(() => callback(undefined, context, undefined)).toThrow(
            'Invalid decorator usage: GetEventContext',
        );
    });
});
