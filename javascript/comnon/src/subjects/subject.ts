import { Subscriber, Unsubscribe } from './types';

export class Subject<T> {
    #isClosed: boolean;

    readonly #observers: Set<Subscriber<T>>;

    constructor () {
        this.#isClosed = false;
        this.#observers = new Set<Subscriber<T>>();
    }

    /**
     * @desc Returns the number of subscribers
     */
    get size () {
        return this.#observers.size;
    }

    /**
     * @desc Subscribes to a subject
     * @param observer - The observer to subscribe
     */
    subscribe (observer: Subscriber<T>): Unsubscribe {
        if (this.#isClosed) {
            throw new Error('Cannot subscribe to a closed subject');
        }

        this.#observers.add(observer);

        return () => this.#observers.delete(observer);
    }

    /**
     * @desc Publishes a message to all subscribers
     * @param message - The message to publish
     */
    publish (message: T) {
        this.#observers.forEach((observer) => observer(message));
    }

    /**
     * @desc Closes the subject
     */
    close () {
        this.#observers.clear();
        this.#isClosed = true;
    }
}
