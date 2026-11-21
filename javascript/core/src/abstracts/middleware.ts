import { MiddlewareFunction, NextFunction } from './types';
import { HttpError } from '../errors/httpError';

export class Middleware<Request, Response> {
    readonly #stack: MiddlewareFunction<Request, Response>[] = [];

    constructor (second?: Middleware<Request, Response>) {
        if (second) {
            this.#stack.push(...second.#stack);
        }
    }

    /**
     * @desc Returns the middleware stack length
     */
    public get length () {
        return this.#stack.length;
    }

    /**
     * @desc Adds a middleware function to the middleware stack
     * @param middleware - the middleware function to add
     */
    public use (middleware: MiddlewareFunction<Request, Response> | Middleware<Request, Response>): this {
        if (middleware instanceof Middleware) {
            this.#stack.push(...middleware.#stack);
        } else {
            this.#stack.push(middleware);
        }

        return this;
    }

    /**
     * @desc Runs the middleware stack
     * @param req - the request object
     * @param res - the response object
     * @param final - the final function to call
     */
    public run (req: Request, res: Response, final: NextFunction) {
        let index = 0;
        let settled = false;

        const finish: NextFunction = (err?: HttpError) => {
            if (settled) {
                return;
            }
            settled = true;
            final(err);
        };

        const dispatch = () => {
            if (settled) {
                return;
            }

            if (index >= this.#stack.length) {
                finish();

                return;
            }

            const middleware = this.#stack[index];
            let delegated = false;

            index++;
            const next: NextFunction = (err?: HttpError) => {
                if (delegated || settled) {
                    return;
                }
                delegated = true;

                if (err) {
                    finish(this.#handleError(err));

                    return;
                }

                dispatch();
            };

            try {
                const result = middleware(req, res, next);

                if (result instanceof Promise) {
                    void result.catch((error) => {
                        if (!delegated) {
                            finish(this.#handleError(error));
                        }
                    });
                }
            } catch (error) {
                if (!delegated) {
                    finish(this.#handleError(error));
                }
            }
        };

        dispatch();
    }

    async runAsync (req: Request, res: Response, final: NextFunction): Promise<void> {
        let index = 0;
        let settled = false;

        const finish: NextFunction = (err?: HttpError) => {
            if (settled) {
                return;
            }
            settled = true;
            final(err);
        };

        const dispatch = async (): Promise<void> => {
            if (settled) {
                return;
            }

            if (index >= this.#stack.length) {
                finish();

                return;
            }

            const middleware = this.#stack[index];
            let delegated = false;
            let delegatedWork: Promise<void> | null = null;

            index++;
            const next: NextFunction = (err?: HttpError) => {
                if (delegated || settled) {
                    return;
                }
                delegated = true;

                if (err) {
                    finish(this.#handleError(err));
                    delegatedWork = Promise.resolve();

                    return;
                }

                delegatedWork = dispatch();
            };

            try {
                await middleware(req, res, next);
            } catch (error) {
                if (!delegated) {
                    finish(this.#handleError(error));
                }
            }

            await delegatedWork;
        };

        await dispatch();
    }

    #handleError (error: unknown): HttpError {
        if (error instanceof HttpError) {
            return error;
        }

        return new HttpError(500, error instanceof Error ? error.message : 'An error occurred while processing the request');
    }
}
