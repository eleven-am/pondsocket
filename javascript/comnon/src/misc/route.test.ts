import { buildPondRoute, compilePondRoute, parsePondRoute, PondRouteError } from './route';

describe('Pond routes', () => {
    it('parses path and wildcard parameters', () => {
        expect(parsePondRoute('/rooms/:roomId/*', '/rooms/general/history/42?limit=5')).toEqual({
            params: {
                roomId: 'general',
                '*': '/history/42',
            },
            query: {
                limit: '5',
            },
        });
    });

    it('escapes literal regular-expression characters', () => {
        expect(parsePondRoute('/rooms/v1.0', '/rooms/v1x0')).toBeNull();
        expect(parsePondRoute('/rooms/v1.0', '/rooms/v1.0')).not.toBeNull();
    });

    it('builds a concrete route from typed parameters', () => {
        expect(buildPondRoute('/rooms/:roomId/*', {
            roomId: 'general chat',
            '*': 'history/42',
        })).toBe('/rooms/general%20chat/history/42');
    });

    it.each([
        'job:*',
        '/rooms/foo:bar',
        '/rooms/:',
        '/rooms/:roomId/:roomId',
        '/rooms/part*',
    ])('rejects unsupported pattern %s', (pattern) => {
        expect(() => compilePondRoute(pattern)).toThrow(PondRouteError);
    });
});
