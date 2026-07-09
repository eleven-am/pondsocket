export type Unsubscribe = () => void;
export type Subscriber<T> = (message: T) => void;
export type SubjectErrorHandler<T> = (error: unknown, observer: Subscriber<T>) => void;
