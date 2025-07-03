import { manageEndpointInstance } from '../managers/endpointInstance';

export function EndpointInstance (): PropertyDecorator {
    return (target, propertyKey) => {
        const { build } = manageEndpointInstance(target);

        build(propertyKey as string);
    };
}
