import { uuid } from './uuid';

describe('uuid', () => {
    it('should return a string', () => {
        const result = uuid();

        expect(typeof result).toBe('string');
    });

    it('should return a string with a length of 36', () => {
        const result = uuid();

        expect(result.length).toBe(36);
    });

    it('should return a string with 4 hyphens', () => {
        const result = uuid();

        expect(result.split('-').length).toBe(5);
    });

    it('should match the UUID v4 format', () => {
        const result = uuid();
        const uuidV4Regex = /^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

        expect(result).toMatch(uuidV4Regex);
    });

    it('should generate unique values', () => {
        const results = new Set(Array.from({ length: 100 }, () => uuid()));

        expect(results.size).toBe(100);
    });
});
