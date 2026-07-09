# PondSocket Common

Shared TypeScript definitions and runtime primitives used by the PondSocket server and clients.

Most applications should install `@eleven-am/pondsocket` or `@eleven-am/pondsocket-client`; those packages install this package automatically. Import it directly when sharing schemas between a server and a client:

```ts
import { definePondChannel, definePondSchema } from '@eleven-am/pondsocket-common';

interface ChatSchema {
    events: {
        message: {
            request: { text: string };
            response: { delivered: boolean };
        };
    };
    assigns: { userId: string };
    presence: { online: boolean };
}

export const Chat = definePondChannel(
    definePondSchema<ChatSchema>(),
    '/chat/:roomId',
);
```
