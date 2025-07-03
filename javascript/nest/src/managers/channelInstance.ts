import type { PondChannel } from '@eleven-am/pondsocket/types';

import { manageProperty } from './property';
import { channelInstanceKey } from '../constants';

export function manageChannelInstance (target: unknown) {
    return manageProperty<PondChannel>(channelInstanceKey, target);
}
