export type Unsubscribe = () => void;
export type Subscriber<T> = (message: T) => unknown;
export type SubjectErrorHandler<T> = (error: unknown, observer: Subscriber<T>) => void;
