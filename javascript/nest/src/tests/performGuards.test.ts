import 'reflect-metadata';
import { manageParameters } from '../managers/parametres';

jest.mock('../helpers/misc', () => ({
    retrieveInstance: (_moduleRef: any, Guard: any) => new Guard(),
}));

jest.mock('../managers/guards', () => {
    let classGuards: any[] = [];
    let methodGuards: any[] = [];

    return {
        manageGuards: (...args: any[]) => ({
            get: () => (args.length === 1 ? classGuards : methodGuards),
        }),
        __setClassGuards: (guards: any[]) => { classGuards = guards; },
        __setMethodGuards: (guards: any[]) => { methodGuards = guards; },
    };
});

const { __setClassGuards, __setMethodGuards } = jest.requireMock('../managers/guards') as any;

async function importPerformAction() {
    const mod = await import('../performers/action');
    return mod;
}

function createModuleRef(): any {
    return {
        get: jest.fn(),
    };
}

function createJoinInput() {
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

describe('performGuards (via performAction)', () => {
    beforeEach(() => {
        __setClassGuards([]);
        __setMethodGuards([]);
    });

    it('returns true and executes handler when no guards provided', async () => {
        const handler = jest.fn().mockResolvedValue(null);
        const instance = { constructor: class TestInstance {}, handler };
        const moduleRef = createModuleRef();
        const input = createJoinInput();

        const { performAction } = await importPerformAction();

        await performAction(instance, moduleRef, [], [], handler, 'handler', input as any, 'join');

        expect(handler).toHaveBeenCalledWith();
    });

    it('returns true and executes handler when all guards pass', async () => {
        class PassGuard {
            canActivate() { return true; }
        }

        __setClassGuards([PassGuard]);

        const handler = jest.fn().mockResolvedValue(null);
        const instance = { constructor: class TestInstance {}, handler };
        const moduleRef = createModuleRef();
        const input = createJoinInput();

        const { performAction } = await importPerformAction();

        await performAction(instance, moduleRef, [], [], handler, 'handler', input as any, 'join');

        expect(handler).toHaveBeenCalled();
    });

    it('returns false on first failing guard and does not execute handler', async () => {
        class FailGuard {
            canActivate() { return false; }
        }

        const handler = jest.fn().mockResolvedValue(null);
        const instance = { constructor: class TestInstance {}, handler };
        const moduleRef = createModuleRef();
        const input = createJoinInput();

        const { performAction } = await importPerformAction();

        await performAction(instance, moduleRef, [FailGuard as any], [], handler, 'handler', input as any, 'join');

        expect(handler).not.toHaveBeenCalled();
        expect(input.decline).toHaveBeenCalledWith('Unauthorized', 403);
    });

    it('guards execute sequentially and short-circuit on first failure', async () => {
        const executionOrder: string[] = [];

        class Guard1 {
            canActivate() {
                executionOrder.push('guard1');
                return true;
            }
        }

        class Guard2 {
            canActivate() {
                executionOrder.push('guard2');
                return false;
            }
        }

        class Guard3 {
            canActivate() {
                executionOrder.push('guard3');
                return true;
            }
        }

        const handler = jest.fn().mockResolvedValue(null);
        const instance = { constructor: class TestInstance {}, handler };
        const moduleRef = createModuleRef();
        const input = createJoinInput();

        const { performAction } = await importPerformAction();

        await performAction(
            instance,
            moduleRef,
            [Guard1 as any, Guard2 as any, Guard3 as any],
            [],
            handler,
            'handler',
            input as any,
            'join',
        );

        expect(executionOrder).toEqual(['guard1', 'guard2']);
        expect(handler).not.toHaveBeenCalled();
    });

    it('replies when a guard rejects an event', async () => {
        class FailGuard {
            canActivate() { return false; }
        }

        const input = {
            assign: jest.fn(),
            reply: jest.fn(),
            broadcast: jest.fn(),
            broadcastFrom: jest.fn(),
            broadcastTo: jest.fn(),
            user: { id: 'u1', assigns: {}, presence: {} },
            channel: {},
            event: { params: {}, query: {}, payload: {}, event: 'message' },
        };
        const handler = jest.fn();
        const instance = { constructor: class TestInstance {}, handler };

        const { performAction } = await importPerformAction();

        await performAction(instance, createModuleRef(), [FailGuard as any], [], handler, 'handler', input as any, 'event');

        expect(handler).not.toHaveBeenCalled();
        expect(input.reply).toHaveBeenCalledWith('UNAUTHORIZED_BROADCAST', {
            code: 'UNAUTHORIZED_BROADCAST',
            message: 'Unauthorized',
            status: 403,
        });
    });

    it('preserves explicitly decorated parameter indices', async () => {
        const handler = jest.fn();
        const instance = { constructor: class TestInstance {}, handler };

        manageParameters(instance, 'handler').set(0, async () => 'first');
        manageParameters(instance, 'handler').set(1, async () => 'second');
        manageParameters(instance, 'handler').set(2, async () => 'third');

        const { performAction } = await importPerformAction();

        await performAction(instance, createModuleRef(), [], [], handler, 'handler', createJoinInput() as any, 'join');

        expect(handler).toHaveBeenCalledWith('first', 'second', 'third');
    });

    it('ignores outgoing handler return values', async () => {
        const handler = jest.fn().mockReturnValue({ text: 'trimmed' });
        const instance = { constructor: class TestInstance {}, handler };
        const outgoing = {
            block: jest.fn(),
            payload: { text: ' raw ' },
            user: { id: 'u1', assigns: {}, presence: {} },
            channel: {},
            event: { params: {}, query: {}, payload: { text: ' raw ' }, event: 'message' },
        };

        const { performAction } = await importPerformAction();
        const result = await performAction(instance, createModuleRef(), [], [], handler, 'handler', outgoing as any, 'outgoing');

        expect(result).toBeUndefined();
        expect(handler).toHaveBeenCalledWith();
    });
});
