import { default as BrowserClient } from './browser/client';
import { default as NodeClient } from './node/node';
import { ChannelState } from '@eleven-am/pondsocket-common';
export default typeof window === 'undefined' ? NodeClient : BrowserClient;
export { ChannelState };
