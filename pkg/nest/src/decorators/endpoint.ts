import { manageEndpoint } from '../managers/endpoint';

export function Endpoint (path = '*'): ClassDecorator {
    return (target) => {
        const { set } = manageEndpoint(target);

        set(path);
    };
}
