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
});
