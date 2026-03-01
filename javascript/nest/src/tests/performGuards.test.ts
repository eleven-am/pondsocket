import 'reflect-metadata';
import { Context } from '../context/context';

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

const { __setClassGuards, __setMethodGuards } = require('../managers/guards') as any;

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

        await performAction(instance, moduleRef, [], [], handler, 'handler', input as any);

        expect(handler).toHaveBeenCalled();
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

        await performAction(instance, moduleRef, [], [], handler, 'handler', input as any);

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

        await performAction(instance, moduleRef, [FailGuard as any], [], handler, 'handler', input as any);

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
        );

        expect(executionOrder).toEqual(['guard1', 'guard2']);
        expect(handler).not.toHaveBeenCalled();
    });
});
