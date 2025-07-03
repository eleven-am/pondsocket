import type { EventContext } from '@eleven-am/pondsocket/types';

import { manageHandlers } from './handlers';
import { onEventHandlerKey } from '../constants';

export function manageEvent (target: unknown) {
    return manageHandlers<EventContext<string>>(
        onEventHandlerKey,
        target,
    );
}
