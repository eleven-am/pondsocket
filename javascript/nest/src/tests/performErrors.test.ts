import { HttpException, HttpStatus } from '@nestjs/common';
import { performErrors } from '../performers/errors';

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

describe('performErrors', () => {
    describe('event context', () => {
        it('replies with UNKNOWN_ERROR for HttpException', () => {
            const ctx = createEventContext();
            const error = new HttpException({ message: 'Validation failed' }, HttpStatus.BAD_REQUEST);

            performErrors(error, ctx as any);

            expect(ctx.reply).toHaveBeenCalledWith('UNKNOWN_ERROR', {
                message: 'Validation failed',
                data: 'Validation failed',
                status: 400,
            });
        });

        it('replies with UNKNOWN_ERROR for generic Error', () => {
            const ctx = createEventContext();
            const error = new Error('Something broke');

            performErrors(error, ctx as any);

            expect(ctx.reply).toHaveBeenCalledWith('UNKNOWN_ERROR', {
                message: 'Something broke',
                data: undefined,
                status: 500,
            });
        });

        it('replies with UNKNOWN_ERROR for non-Error values', () => {
            const ctx = createEventContext();

            performErrors('string error', ctx as any);

            expect(ctx.reply).toHaveBeenCalledWith('UNKNOWN_ERROR', {
                message: 'An unknown error occurred',
                data: undefined,
                status: 500,
            });
        });
    });

    describe('connection context', () => {
        it('calls decline with error info for HttpException', () => {
            const ctx = createConnectionContext();
            const error = new HttpException('Forbidden', HttpStatus.FORBIDDEN);

            performErrors(error, ctx as any);

            expect(ctx.decline).toHaveBeenCalledWith('Forbidden', 403);
        });

        it('calls decline with message and status 500 for generic Error', () => {
            const ctx = createConnectionContext();
            const error = new Error('Connection failed');

            performErrors(error, ctx as any);

            expect(ctx.decline).toHaveBeenCalledWith('Connection failed', 500);
        });

        it('does not call decline if hasResponded is true', () => {
            const ctx = createConnectionContext();
            ctx.hasResponded = true;
            const error = new Error('Too late');

            performErrors(error, ctx as any);

            expect(ctx.decline).not.toHaveBeenCalled();
        });
    });

    describe('join context', () => {
        it('calls decline with error info', () => {
            const ctx = createJoinContext();
            const error = new HttpException('Not allowed', HttpStatus.UNAUTHORIZED);

            performErrors(error, ctx as any);

            expect(ctx.decline).toHaveBeenCalledWith('Not allowed', 401);
        });

        it('does not call decline if hasResponded is true', () => {
            const ctx = createJoinContext();
            ctx.hasResponded = true;
            const error = new Error('Already responded');

            performErrors(error, ctx as any);

            expect(ctx.decline).not.toHaveBeenCalled();
        });
    });

    describe('HttpException with nested response message', () => {
        it('extracts data from response message array', () => {
            const ctx = createEventContext();
            const error = new HttpException(
                { message: ['field1 is required', 'field2 must be a string'], error: 'Bad Request' },
                HttpStatus.BAD_REQUEST,
            );

            performErrors(error, ctx as any);

            expect(ctx.reply).toHaveBeenCalledWith('UNKNOWN_ERROR', expect.objectContaining({
                status: 400,
                data: ['field1 is required', 'field2 must be a string'],
            }));
        });
    });
});
