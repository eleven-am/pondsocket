import 'reflect-metadata';

import { createParamDecorator } from '../helpers/createParamDecorator';
import { manageParameters } from '../managers/parametres';

describe('createParamDecorator', () => {
    it('does not require emitted design parameter metadata', () => {
        const decorator = createParamDecorator((data: string) => data);

        class Controller {
            handler (_value: string) {}
        }

        Reflect.deleteMetadata('design:paramtypes', Controller.prototype, 'handler');

        expect(() => decorator('value')(Controller.prototype, 'handler', 0)).not.toThrow();
        expect(manageParameters(Controller.prototype, 'handler').get()).toHaveLength(1);
    });
});
