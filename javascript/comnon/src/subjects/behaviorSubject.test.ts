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

    it('should return undefined when constructed without initial value', () => {
        const sub = new BehaviorSubject<number>();

        expect(sub.value).toBeUndefined();
    });

    it('should not call subscriber on subscribe when no initial value', () => {
        const sub = new BehaviorSubject<number>();
        const obs = jest.fn();

        sub.subscribe(obs);
        expect(obs).not.toHaveBeenCalled();
    });

    it('should return the initial value before any publish', () => {
        expect(testSubject.value).toBe(0);
    });

    it('should track the latest published value', () => {
        testSubject.publish(5);
        testSubject.publish(10);
        testSubject.publish(15);
        expect(testSubject.value).toBe(15);
    });

    it('should call new subscriber with latest value immediately', () => {
        testSubject.publish(42);
        const laterObserver = jest.fn();

        testSubject.subscribe(laterObserver);
        expect(laterObserver).toHaveBeenCalledWith(42);
        expect(laterObserver).toHaveBeenCalledTimes(1);
    });

    it('should close and prevent further subscriptions', () => {
        testSubject.close();
        expect(() => testSubject.subscribe(jest.fn())).toThrow('Cannot subscribe to a closed subject');
    });
});
