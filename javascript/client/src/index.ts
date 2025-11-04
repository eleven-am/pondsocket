import { ChannelState } from '@eleven-am/pondsocket-common';

import { PondClient as BrowserClient } from './browser/client';
import { PondClient as NodeClient } from './node/node';

const PondClient = typeof window === 'undefined' ? NodeClient : BrowserClient;

export { ChannelState, PondClient };
