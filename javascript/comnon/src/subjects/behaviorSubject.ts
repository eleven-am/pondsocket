import { Subject } from './subject';
import { Unsubscribe, Subscriber, SubjectErrorHandler } from './types';

export class BehaviorSubject<T> extends Subject<T> {
    #lastMessage: T | undefined;

    constructor (initialValue?: T, onObserverError?: SubjectErrorHandler<T>) {
        super(onObserverError);

        this.#lastMessage = initialValue;
    }

    /**
     * @desc Returns the last message published
     */
    public get value () {
        return this.#lastMessage;
    }

    /**
     * @desc Publishes a message to all subscribers
     * @param message - The message to publish
     */
    publish (message: T) {
        super.publish(message);
    }

    /**
     * @desc Subscribes to a subject
     * @param observer - The observer to subscribe
     */
    subscribe (observer: Subscriber<T>): Unsubscribe {
        const unsubscribe = super.subscribe(observer);

        if (this.#lastMessage !== undefined) {
            this.notifyObserver(observer, this.#lastMessage);
        }

        return unsubscribe;
    }

    protected beforePublish (message: T): void {
        this.#lastMessage = message;
    }
}
