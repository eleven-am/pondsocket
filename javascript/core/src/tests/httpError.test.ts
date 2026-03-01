import { HttpError } from '../errors/httpError';

describe('HttpError', () => {
    it('should set name, statusCode, message, and statusText', () => {
        const error = new HttpError(404, 'Not Found');

        expect(error.name).toBe('HttpError');
        expect(error.statusCode).toBe(404);
        expect(error.message).toBe('Not Found');
        expect(error.statusText).toBe('Not Found');
    });

    it('should be an instance of Error', () => {
        const error = new HttpError(500, 'Server Error');

        expect(error).toBeInstanceOf(Error);
        expect(error).toBeInstanceOf(HttpError);
    });

    describe('getStatusText', () => {
        const cases: [number, string][] = [
            [400, 'Bad Request'],
            [401, 'Unauthorized'],
            [403, 'Forbidden'],
            [404, 'Not Found'],
            [405, 'Method Not Allowed'],
            [406, 'Not Acceptable'],
            [409, 'Conflict'],
            [413, 'Payload Too Large'],
            [429, 'Too Many Requests'],
            [500, 'Internal Server Error'],
            [501, 'Not Implemented'],
            [502, 'Bad Gateway'],
            [503, 'Service Unavailable'],
            [504, 'Gateway Timeout'],
            [999, 'Unknown Error'],
        ];

        cases.forEach(([code, expected]) => {
            it(`should return "${expected}" for status code ${code}`, () => {
                expect(HttpError.getStatusText(code)).toBe(expected);
            });
        });
    });
});
