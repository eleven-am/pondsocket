import { ChannelState } from '@eleven-am/pondsocket-common';

import { PondClient as BrowserClient } from './browser/client';
import { SSEClient } from './browser/sseClient';
import { PondClient as NodeClient } from './node/node';
import { ConnectionState } from './types';

const PondClient = typeof window === 'undefined' ? NodeClient : BrowserClient;

export { ChannelState, ConnectionState, PondClient, SSEClient };
