import 'reflect-metadata';
import { createMetadataManager } from '../managers/createMetadataManager';

describe('createMetadataManager', () => {
    const testKey = Symbol('test_metadata');

    beforeEach(() => {
        jest.restoreAllMocks();
    });

    it('accumulates values with set on a class target', () => {
        class TestClass {}
        const { manage } = createMetadataManager<any>(testKey);

        const manager = manage(TestClass);

        class GuardA {}
        class GuardB {}

        manager.set([GuardA as any]);
        manager.set([GuardB as any]);

        const result = manager.get();
        expect(result).toEqual([GuardB, GuardA]);
    });

    it('accumulates values with set on a method target', () => {
        class TestClass {
            handler() {}
        }
        const methodKey = Symbol('test_method_metadata');
        const { manage } = createMetadataManager<any>(methodKey);
        const instance = new TestClass();

        const manager = manage(instance, 'handler');

        class PipeA {}
        class PipeB {}

        manager.set([PipeA as any]);
        manager.set([PipeB as any]);

        const result = manager.get();
        expect(result).toEqual([PipeB, PipeA]);
    });

    it('getLocalItems tracks all registered constructors', () => {
        const localKey = Symbol('local_metadata');
        const { manage, getLocalItems } = createMetadataManager<any>(localKey);

        class TestClass {}
        class ItemA {}
        class ItemB {}
        class ItemC {}

        const manager = manage(TestClass);
        manager.set([ItemA as any, ItemB as any]);
        manager.set([ItemC as any]);

        const locals = getLocalItems();
        expect(locals).toContain(ItemA);
        expect(locals).toContain(ItemB);
        expect(locals).toContain(ItemC);
        expect(locals).toHaveLength(3);
    });

    it('returns empty array when nothing set', () => {
        const emptyKey = Symbol('empty_metadata');
        const { manage } = createMetadataManager<any>(emptyKey);

        class TestClass {}
        const manager = manage(TestClass);

        expect(manager.get()).toEqual([]);
    });

    it('set prepends new values before existing ones', () => {
        const prependKey = Symbol('prepend_metadata');
        const { manage } = createMetadataManager<any>(prependKey);

        class TestClass {}
        class First {}
        class Second {}

        const manager = manage(TestClass);
        manager.set([First as any]);
        manager.set([Second as any]);

        const result = manager.get();
        expect(result[0]).toBe(Second);
        expect(result[1]).toBe(First);
    });
});
