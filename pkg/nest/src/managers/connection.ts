import type { ConnectionContext } from '@eleven-am/pondsocket/types';

import { onConnectionHandlerKey } from '../constants';
import { manageHandlers } from './handlers';


export function manageConnection (target: unknown) {
    return manageHandlers<ConnectionContext<string>>(
        onConnectionHandlerKey,
        target,
    );
}
