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
});
