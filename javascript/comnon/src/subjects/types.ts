export type Unsubscribe = () => void;
export type Subscriber<T> = (message: T) => void;
