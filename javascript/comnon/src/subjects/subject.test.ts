import { Subject } from './subject';

describe('Subject', () => {
    let testSubject: Subject<number>;
    let observer1: jest.Mock;
    let observer2: jest.Mock;

    beforeEach(() => {
        testSubject = new Subject<number>();
        observer1 = jest.fn();
        observer2 = jest.fn();
        testSubject.subscribe(observer1);
        testSubject.subscribe(observer2);
    });

    afterEach(() => {
        testSubject.publish(0);
    });

    it('should notify all subscribers when publish is called', () => {
        const message = 10;

        testSubject.publish(message);
        expect(observer1).toHaveBeenCalledWith(message);
        expect(observer2).toHaveBeenCalledWith(message);
    });

    it('should unsubscribe an observer when unsubscribe is called', () => {
        const unsubscribe = testSubject.subscribe(observer1);

        unsubscribe();
        expect(testSubject.size).toBe(1);
    });

    it('should throw an error when trying to subscribe to a closed subject', () => {
        testSubject.close();
        expect(() => testSubject.subscribe(observer1)).toThrow('Cannot subscribe to a closed subject');
    });

    it('should clear all observers on close', () => {
        expect(testSubject.size).toBe(2);
        testSubject.close();
        expect(testSubject.size).toBe(0);
    });

    it('should not notify observers after close', () => {
        testSubject.close();
        testSubject.publish(42);
        expect(observer1).not.toHaveBeenCalled();
        expect(observer2).not.toHaveBeenCalled();
    });

    it('should handle publishing with no subscribers', () => {
        const emptySubject = new Subject<number>();

        expect(() => emptySubject.publish(10)).not.toThrow();
    });

    it('should isolate subscriber errors and continue fan-out', () => {
        const onError = jest.fn();
        const subject = new Subject<number>(onError);
        const second = jest.fn();
        const error = new Error('subscriber failed');

        subject.subscribe(() => {
            throw error;
        });
        subject.subscribe(second);
        subject.publish(42);

        expect(onError).toHaveBeenCalledWith(error, expect.any(Function));
        expect(second).toHaveBeenCalledWith(42);
    });

    it('should report rejected async subscribers without an unhandled rejection', async () => {
        const onError = jest.fn();
        const subject = new Subject<number>(onError);
        const error = new Error('async subscriber failed');

        subject.subscribe(async () => {
            throw error;
        });
        subject.publish(42);
        await Promise.resolve();

        expect(onError).toHaveBeenCalledWith(error, expect.any(Function));
    });

    it('should handle double unsubscribe gracefully', () => {
        const newObserver = jest.fn();
        const unsub = testSubject.subscribe(newObserver);

        unsub();
        expect(() => unsub()).not.toThrow();
    });

    it('should track size correctly through subscribe and unsubscribe', () => {
        const sub = new Subject<string>();

        expect(sub.size).toBe(0);
        const unsub1 = sub.subscribe(jest.fn());

        expect(sub.size).toBe(1);
        const unsub2 = sub.subscribe(jest.fn());

        expect(sub.size).toBe(2);
        unsub1();
        expect(sub.size).toBe(1);
        unsub2();
        expect(sub.size).toBe(0);
    });

    it('should defer subscribers added during publication until the next publication', () => {
        const subject = new Subject<number>();
        const lateObserver = jest.fn();

        subject.subscribe(() => {
            subject.subscribe(lateObserver);
        });
        subject.publish(1);
        expect(lateObserver).not.toHaveBeenCalled();

        subject.publish(2);
        expect(lateObserver).toHaveBeenCalledTimes(1);
        expect(lateObserver).toHaveBeenCalledWith(2);
    });

    it('should preserve observer order when a subscriber publishes reentrantly', () => {
        const subject = new Subject<number>();
        const received: string[] = [];

        subject.subscribe((message) => {
            received.push(`first:${message}`);
            if (message === 1) {
                subject.publish(2);
            }
        });
        subject.subscribe((message) => {
            received.push(`second:${message}`);
        });

        subject.publish(1);

        expect(received).toEqual([
            'first:1',
            'second:1',
            'first:2',
            'second:2',
        ]);
    });
});
