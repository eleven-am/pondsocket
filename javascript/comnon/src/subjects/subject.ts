import { Subscriber, SubjectErrorHandler, Unsubscribe } from './types';

const ignoreObserverError = () => undefined;

export class Subject<T> {
    #isClosed: boolean;

    readonly #observers: Set<Subscriber<T>>;

    readonly #onObserverError: SubjectErrorHandler<T>;

    constructor (onObserverError: SubjectErrorHandler<T> = ignoreObserverError) {
        this.#isClosed = false;
        this.#observers = new Set<Subscriber<T>>();
        this.#onObserverError = onObserverError;
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
        this.#observers.forEach((observer) => this.notifyObserver(observer, message));
    }

    /**
     * @desc Closes the subject
     */
    close () {
        this.#observers.clear();
        this.#isClosed = true;
    }

    protected notifyObserver (observer: Subscriber<T>, message: T) {
        try {
            observer(message);
        } catch (error) {
            this.#onObserverError(error, observer);
        }
    }
}
