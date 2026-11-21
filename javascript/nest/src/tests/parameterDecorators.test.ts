import 'reflect-metadata';

import {
    definePondChannel,
    definePondSchema,
    PondSchema,
} from '@eleven-am/pondsocket-common';

import { Context } from '../context/context';
import {
    GetEventParams,
    GetEventPayload,
} from '../decorators';
import { managePipes } from '../managers/pipes';
import { performAction } from '../performers/action';

function createModuleRef (): any {
    return { get: jest.fn() };
}

function createEventInput () {
    return {
        assign: jest.fn(),
        reply: jest.fn(),
        broadcast: jest.fn(),
        broadcastFrom: jest.fn(),
        broadcastTo: jest.fn(),
        user: { id: 'u1', assigns: {}, presence: {} },
        channel: { name: '/chat/general' },
        event: {
            params: { messageId: 'message-1' },
            query: { trace: 'trace-1' },
            payload: { text: 'hello' },
            event: 'message/message-1',
        },
    };
}

describe('PondSocket parameter decorators', () => {
    interface ChatSchema extends PondSchema<{
        'message/:messageId': { text: string };
    }> {}

    const Chat = definePondChannel(
        definePondSchema<ChatSchema>(),
        '/chat/:roomId',
    );

    it('injects event data at its declared parameter positions', async () => {
        const received: unknown[][] = [];

        class Controller {
            handler (
                @GetEventParams() eventParams: Record<string, string>,
                @GetEventPayload() payload: Record<string, unknown>,
                @Chat.GetContext() context: Context,
            ) {
                received.push([eventParams, payload, context]);
            }
        }

        const controller = new Controller();

        await performAction(
            controller,
            createModuleRef(),
            [],
            [],
            controller.handler,
            'handler',
            createEventInput() as any,
            'event',
            Chat,
        );

        expect(received).toEqual([[
            { messageId: 'message-1' },
            { text: 'hello' },
            expect.any(Context),
        ]]);
    });

    it('rejects a context decorator bound to a different channel', async () => {
        const OtherChat = definePondChannel(
            definePondSchema<ChatSchema>(),
            '/other/:roomId',
        );

        class Controller {
            handler (@OtherChat.GetContext() _context: Context) {}
        }

        const controller = new Controller();

        await expect(performAction(
            controller,
            createModuleRef(),
            [],
            [],
            controller.handler,
            'handler',
            createEventInput() as any,
            'event',
            Chat,
        )).rejects.toThrow(
            'Controller.handler uses GetContext() from a different PondSocket definition',
        );
    });

    it('rejects every undecorated handler parameter', async () => {
        class Controller {
            handler (_context: Context) {}
        }

        const controller = new Controller();

        await expect(performAction(
            controller,
            createModuleRef(),
            [],
            [],
            controller.handler,
            'handler',
            createEventInput() as any,
            'event',
        )).rejects.toThrow(
            'Controller.handler parameter at index 0 must use a PondSocket parameter decorator',
        );
    });

    it('runs each pipe binding across global, class, and method scopes', async () => {
        const calls: unknown[] = [];

        class SharedPipe {
            transform (value: unknown) {
                calls.push(value);

                return value;
            }
        }

        class Controller {
            handler (@GetEventPayload() _payload: Record<string, unknown>) {}
        }

        const controller = new Controller();
        managePipes(Controller).set([SharedPipe]);
        managePipes(controller, 'handler').set([SharedPipe]);
        const moduleRef = {
            get: jest.fn((Type: typeof SharedPipe) => new Type()),
            registerRequestByContextId: jest.fn(),
        };

        await performAction(
            controller,
            moduleRef as any,
            [],
            [SharedPipe],
            controller.handler,
            'handler',
            createEventInput() as any,
            'event',
        );

        expect(calls).toEqual([
            { text: 'hello' },
            { text: 'hello' },
            { text: 'hello' },
        ]);
    });
});
