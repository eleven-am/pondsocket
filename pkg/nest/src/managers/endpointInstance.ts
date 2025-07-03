import type { Endpoint } from '@eleven-am/pondsocket/types';

import { manageProperty } from './property';
import { endpointInstanceKey } from '../constants';

export function manageEndpointInstance (target: unknown) {
    return manageProperty<Endpoint>(endpointInstanceKey, target);
}
