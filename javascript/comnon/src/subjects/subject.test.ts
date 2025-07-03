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
        expect(() => testSubject.subscribe(observer1)).toThrowError('Cannot subscribe to a closed subject');
    });
});
