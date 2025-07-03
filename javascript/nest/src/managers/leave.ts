import type { LeaveEvent } from '@eleven-am/pondsocket/types';

import { manageHandlers } from './handlers';
import { onLeaveHandlerKey } from '../constants';

export function manageLeave (target: unknown) {
    return manageHandlers<LeaveEvent>(
        onLeaveHandlerKey,
        target,
    );
}
