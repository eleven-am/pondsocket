import { OutgoingContext } from '../contexts/outgoingContext';
import { MockChannelEngine } from './mocks/channelEnegine';

describe('OutgoingContext', () => {
    let context: OutgoingContext<string>;
    let engine: MockChannelEngine;
    const event = { event: 'test-event', payload: { data: 'hello' }, action: 'broadcast' as any, channelName: 'test-channel', requestId: 'req-1' };
    const params = { params: {}, query: {} };
    const userId = 'user-1';

    beforeEach(() => {
        engine = new MockChannelEngine();
        context = new OutgoingContext(event, params, engine, userId);
    });

    describe('payload', () => {
        it('should return the original payload', () => {
            expect(context.payload).toEqual({ data: 'hello' });
        });
    });

    describe('block', () => {
        it('should mark the context as blocked', () => {
            expect(context.isBlocked()).toBe(false);
            context.block();
            expect(context.isBlocked()).toBe(true);
        });

        it('should return this for chaining', () => {
            const result = context.block();

            expect(result).toBe(context);
        });
    });

    describe('isBlocked', () => {
        it('should return false by default', () => {
            expect(context.isBlocked()).toBe(false);
        });
    });

    describe('transform', () => {
        it('should update the payload', () => {
            const newPayload = { transformed: true };

            context.transform(newPayload);
            expect(context.payload).toEqual(newPayload);
        });

        it('should return this for chaining', () => {
            const result = context.transform({ new: 'data' });

            expect(result).toBe(context);
        });
    });

    describe('updateParams', () => {
        it('should update event params', () => {
            const newParams = { params: { id: '123' }, query: { filter: 'active' } };

            context.updateParams(newParams);
            expect(context.event.params).toEqual({ id: '123' });
            expect(context.event.query).toEqual({ filter: 'active' });
        });
    });
});
