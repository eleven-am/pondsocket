import { manageClass } from './class';
import { endpointKey } from '../constants';

export function manageEndpoint (target: unknown) {
    return manageClass<string>(endpointKey, target);
}
