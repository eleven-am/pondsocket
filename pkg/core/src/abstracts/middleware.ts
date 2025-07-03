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

        const next: NextFunction = (err?: HttpError) => {
            if (err) {
                return final(this.#handleError(err));
            }

            if (index >= this.#stack.length) {
                return final();
            }

            const middleware = this.#stack[index];

            index++;

            try {
                const result = middleware(req, res, next);

                if (result instanceof Promise) {
                    result.catch(next);
                }
            } catch (error) {
                return final(this.#handleError(error));
            }
        };

        next();
    }

    runAsync (req: Request, res: Response, final: NextFunction): Promise<void> {
        let index = 0;

        const next = async (err?: HttpError) => {
            if (err) {
                return final(this.#handleError(err));
            }

            if (index >= this.#stack.length) {
                return final();
            }

            const middleware = this.#stack[index];

            index++;

            try {
                await middleware(req, res, next);
            } catch (error) {
                return final(this.#handleError(error));
            }
        };

        return next();
    }

    #handleError (error: unknown): HttpError {
        if (error instanceof HttpError) {
            return error;
        }

        return new HttpError(500, error instanceof Error ? error.message : 'An error occurred while processing the request');
    }
}
