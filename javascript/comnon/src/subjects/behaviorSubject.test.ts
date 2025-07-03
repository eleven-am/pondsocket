import { BehaviorSubject } from './behaviorSubject';

describe('behaviorSubject', () => {
    let testSubject: BehaviorSubject<number>;
    let observer1: jest.Mock;
    let observer2: jest.Mock;

    beforeEach(() => {
        testSubject = new BehaviorSubject<number>(0);
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

    it('should notify new subscribers with the last message when subscribe is called', () => {
        const message = 10;

        testSubject.publish(message);
        const newObserver: jest.Mock = jest.fn();

        testSubject.subscribe(newObserver);
        expect(newObserver).toHaveBeenCalledWith(message);
    });

    it('should return the last message when value is called', () => {
        const message = 10;

        testSubject.publish(message);
        expect(testSubject.value).toBe(message);
    });
});
