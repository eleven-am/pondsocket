import type { JoinContext } from '@eleven-am/pondsocket/types';

import { manageHandlers } from './handlers';
import { onJoinHandlerKey } from '../constants';

export function manageJoin (target: unknown) {
    return manageHandlers<JoinContext<string>>(
        onJoinHandlerKey,
        target,
    );
}
