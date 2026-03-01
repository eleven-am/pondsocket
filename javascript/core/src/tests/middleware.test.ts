import { Middleware } from '../abstracts/middleware';
import { MiddlewareFunction } from '../abstracts/types';
import { HttpError } from '../errors/httpError';

describe('Middleware', () => {
    let middleware: Middleware<any, any>;

    beforeEach(() => {
        middleware = new Middleware();
    });

    describe('constructor', () => {
        it('should create an empty middleware stack', () => {
            expect(middleware.length).toBe(0);
        });

        it('should merge middleware from another instance if provided', () => {
            const firstMiddleware = new Middleware();

            firstMiddleware.use(() => {});
            firstMiddleware.use(() => {});

            const secondMiddleware = new Middleware(firstMiddleware);

            expect(secondMiddleware.length).toBe(2);
        });
    });

    describe('use', () => {
        it('should add a middleware function to the stack', () => {
            const middlewareFunction = jest.fn();

            middleware.use(middlewareFunction);
            expect(middleware.length).toBe(1);
        });

        it('should add multiple middleware functions from another Middleware instance', () => {
            const otherMiddleware = new Middleware();

            otherMiddleware.use(() => {});
            otherMiddleware.use(() => {});

            middleware.use(otherMiddleware);
            expect(middleware.length).toBe(2);
        });

        it('should return the Middleware instance for chaining', () => {
            const result = middleware.use(() => {});

            expect(result).toBe(middleware);
        });
    });

    describe('run', () => {
        it('should execute middleware functions in order', () => {
            const order: number[] = [];
            const middleware1: MiddlewareFunction<any, any> = (req, res, next) => {
                order.push(1);
                next();
            };
            const middleware2: MiddlewareFunction<any, any> = (req, res, next) => {
                order.push(2);
                next();
            };

            middleware.use(middleware1);
            middleware.use(middleware2);

            middleware.run({}, {}, () => {
                order.push(3);
            });

            expect(order).toEqual([1, 2, 3]);
        });

        it('should stop execution if next is not called', () => {
            const order: number[] = [];
            const middleware1: MiddlewareFunction<any, any> = (req, res, next) => {
                order.push(1);
                // next() is not called
            };
            const middleware2: MiddlewareFunction<any, any> = (req, res, next) => {
                order.push(2);
                next();
            };

            middleware.use(middleware1);
            middleware.use(middleware2);

            middleware.run({}, {}, () => {
                order.push(3);
            });

            expect(order).toEqual([1]);
        });

        it('should handle errors thrown in middleware', () => {
            const errorMiddleware: MiddlewareFunction<any, any> = () => {
                throw new Error('Test error');
            };

            middleware.use(errorMiddleware);

            const finalFn = jest.fn();

            middleware.run({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].message).toBe('Test error');
            expect(finalFn.mock.calls[0][0].statusCode).toBe(500);
        });

        it('should handle async middleware', (done) => {
            const asyncMiddleware: MiddlewareFunction<any, any> = async (req, res, next) => {
                await new Promise((resolve) => setTimeout(resolve, 10));
                next();
            };

            middleware.use(asyncMiddleware);

            const finalFn = jest.fn(() => {
                expect(finalFn).toHaveBeenCalled();
                done();
            });

            middleware.run({}, {}, finalFn);
        });

        it('should handle errors in async middleware', (done) => {
            const asyncErrorMiddleware: MiddlewareFunction<any, any> = async () => {
                await new Promise((resolve) => setTimeout(resolve, 10));
                throw new Error('Async error');
            };

            middleware.use(asyncErrorMiddleware);

            const finalFn = jest.fn((error) => {
                expect(error).toBeInstanceOf(HttpError);
                expect(error.message).toBe('Async error');
                expect(error.statusCode).toBe(500);
                done();
            });

            middleware.run({}, {}, finalFn);
        });

        it('should pass HttpError instances through without wrapping', async () => {
            const httpErrorMiddleware: MiddlewareFunction<any, any> = () => {
                throw new HttpError(403, 'Forbidden');
            };

            middleware.use(httpErrorMiddleware);

            const finalFn = jest.fn();

            await middleware.run({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].message).toBe('Forbidden');
            expect(finalFn.mock.calls[0][0].statusCode).toBe(403);
        });

        it('should call the final function if the middleware stack is empty', () => {
            const finalFn = jest.fn();

            middleware.run({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalled();
            expect(finalFn).toHaveBeenCalled();
        });

        it('should wrap non-Error thrown values in HttpError', () => {
            const badMiddleware: MiddlewareFunction<any, any> = () => {
                throw 'string error';
            };

            middleware.use(badMiddleware);

            const finalFn = jest.fn();

            middleware.run({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].message).toBe('An error occurred while processing the request');
            expect(finalFn.mock.calls[0][0].statusCode).toBe(500);
        });

        it('should forward HttpError passed to next', () => {
            const errorMiddleware: MiddlewareFunction<any, any> = (_req, _res, next) => {
                next(new HttpError(403, 'Forbidden'));
            };

            middleware.use(errorMiddleware);

            const finalFn = jest.fn();

            middleware.run({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].statusCode).toBe(403);
        });
    });

    describe('runAsync', () => {
        it('should execute middleware functions in order', async () => {
            const order: number[] = [];

            middleware.use(async (_req, _res, next) => {
                order.push(1);
                await next();
            });
            middleware.use(async (_req, _res, next) => {
                order.push(2);
                await next();
            });

            await middleware.runAsync({}, {}, () => {
                order.push(3);
            });

            expect(order).toEqual([1, 2, 3]);
        });

        it('should handle errors thrown in async middleware', async () => {
            middleware.use(async () => {
                throw new Error('Async runAsync error');
            });

            const finalFn = jest.fn();

            await middleware.runAsync({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].message).toBe('Async runAsync error');
        });

        it('should pass HttpError through without wrapping in runAsync', async () => {
            middleware.use(async () => {
                throw new HttpError(401, 'Unauthorized');
            });

            const finalFn = jest.fn();

            await middleware.runAsync({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].statusCode).toBe(401);
        });

        it('should call final when stack is empty', async () => {
            const finalFn = jest.fn();

            await middleware.runAsync({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalled();
        });

        it('should handle error passed to next in runAsync', async () => {
            middleware.use(async (_req, _res, next) => {
                await next(new HttpError(422, 'Unprocessable'));
            });

            const finalFn = jest.fn();

            await middleware.runAsync({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].statusCode).toBe(422);
        });

        it('should wrap non-Error values thrown in runAsync', async () => {
            middleware.use(async () => {
                throw { weird: 'object' };
            });

            const finalFn = jest.fn();

            await middleware.runAsync({}, {}, finalFn);

            expect(finalFn).toHaveBeenCalledWith(expect.any(HttpError));
            expect(finalFn.mock.calls[0][0].message).toBe('An error occurred while processing the request');
        });
    });
});
