import { Subscriber, SubjectErrorHandler, Unsubscribe } from './types';

const ignoreObserverError = () => undefined;

export class Subject<T> {
    #isClosed: boolean;

    readonly #observers: Set<Subscriber<T>>;

    readonly #onObserverError: SubjectErrorHandler<T>;

    readonly #pendingMessages: T[];

    #isPublishing: boolean;

    constructor (onObserverError: SubjectErrorHandler<T> = ignoreObserverError) {
        this.#isClosed = false;
        this.#observers = new Set<Subscriber<T>>();
        this.#onObserverError = onObserverError;
        this.#pendingMessages = [];
        this.#isPublishing = false;
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
        if (this.#isClosed) {
            return;
        }

        this.#pendingMessages.push(message);
        if (this.#isPublishing) {
            return;
        }

        this.#isPublishing = true;
        let index = 0;

        try {
            while (!this.#isClosed && index < this.#pendingMessages.length) {
                const nextMessage = this.#pendingMessages[index++];

                this.beforePublish(nextMessage);

                // Snapshot each publication so subscription changes made by
                // an observer apply to the next message, never midway through
                // the current fan-out.
                for (const observer of [...this.#observers]) {
                    if (this.#isClosed) {
                        break;
                    }
                    this.notifyObserver(observer, nextMessage);
                }
            }
        } finally {
            this.#pendingMessages.splice(0, index);
            this.#isPublishing = false;
        }
    }

    /**
     * @desc Closes the subject
     */
    close () {
        this.#observers.clear();
        this.#pendingMessages.length = 0;
        this.#isClosed = true;
    }

    protected notifyObserver (observer: Subscriber<T>, message: T) {
        try {
            const result = observer(message);

            void Promise.resolve(result).catch((error) => {
                this.#reportObserverError(error, observer);
            });
        } catch (error) {
            this.#reportObserverError(error, observer);
        }
    }

    protected beforePublish (_message: T): void {
        // Subclasses can synchronize state with the item that is about to be
        // delivered. This runs when queued, reentrant publications actually
        // begin, rather than when they are merely enqueued.
    }

    #reportObserverError (error: unknown, observer: Subscriber<T>): void {
        try {
            this.#onObserverError(error, observer);
        } catch (_) {
            // Error reporting must never break fan-out or create an unhandled
            // rejection of its own.
            void 0;
        }
    }
}
