import { Subject } from './subject';
import { Unsubscribe, Subscriber } from './types';

export class BehaviorSubject<T> extends Subject<T> {
    #lastMessage: T | undefined;

    constructor (initialValue?: T) {
        super();

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
        this.#lastMessage = message;
        super.publish(message);
    }

    /**
     * @desc Subscribes to a subject
     * @param observer - The observer to subscribe
     */
    subscribe (observer: Subscriber<T>): Unsubscribe {
        if (this.#lastMessage) {
            observer(this.#lastMessage);
        }

        return super.subscribe(observer);
    }
}
