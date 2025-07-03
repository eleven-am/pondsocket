import { parseAddress } from '../matcher/matcher';

describe('parseAddress', () => {
    it('should return null if a string does not match a regex and {} if it does', () => {
        const regex = new RegExp(/^test/);

        expect(parseAddress(regex, 'test')).toStrictEqual({
            params: {},
            query: {},
        });

        const regex2 = new RegExp(/^test2/);

        expect(parseAddress(regex2, 'test')).toBe(null);
    });

    it('should return the params of a string matching the pattern', () => {
        const pattern = '/test/:id';
        const secondPattern = '/test/:id/:id2';
        const string = '/test/5';
        const secondString = '/test/5/6';

        expect(parseAddress(pattern, string))
            .toStrictEqual({
                params: {
                    id: '5',
                },
                query: {},
            });

        expect(parseAddress(secondPattern, secondString))
            .toStrictEqual({
                params: {
                    id: '5',
                    id2: '6',
                },
                query: {},
            });

        expect(parseAddress(pattern, secondString))
            .toBe(null);

        expect(parseAddress(secondPattern, string))
            .toBe(null);
    });

    it('should not match colons without slashes', () => {
        const pattern = '/test:id';
        const string = '/test5';

        expect(parseAddress(pattern, string))
            .toBe(null);
    });

    it('should match when there is a *', () => {
        const pattern = '/test/:id/*';
        const string = '/test/5/6';

        expect(parseAddress(pattern, string))
            .toStrictEqual({
                params: {
                    id: '5',
                    '*': '/6',
                },
                query: {},
            });

        const secondPattern = '*';
        const secondString = '/test/5/6';

        expect(parseAddress(secondPattern, secondString))
            .toStrictEqual({
                params: {
                    '*': '/test/5/6',
                },
                query: {},
            });
    });

    it('should return the query of string', () => {
        const pattern = '/test/:id';
        const string = '/test/5?test=5';
        const secondString = '/test/5?test=5&test2=6';

        expect(parseAddress(pattern, string))
            .toStrictEqual({
                params: {
                    id: '5',
                },
                query: {
                    test: '5',
                },
            });

        expect(parseAddress(pattern, secondString))
            .toStrictEqual({
                params: {
                    id: '5',
                },
                query: {
                    test: '5',
                    test2: '6',
                },
            });
    });
});
