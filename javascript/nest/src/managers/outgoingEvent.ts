import type { OutgoingContext } from '@eleven-am/pondsocket/types';

import { manageHandlers } from './handlers';
import { onOutgoingEventHandlerKey } from '../constants';

export function manageOutgoingEvent (target: unknown) {
    return manageHandlers<OutgoingContext<string>>(
        onOutgoingEventHandlerKey,
        target,
    );
}
