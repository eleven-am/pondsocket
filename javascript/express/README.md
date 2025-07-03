# PondSocket Express Integration

This package provides seamless integration of [PondSocket](https://www.npmjs.com/package/@eleven-am/pondsocket) with [Express](https://expressjs.com/), allowing you to add high-performance, bidirectional WebSocket endpoints to your Express applications with minimal effort.

## Installation

Install the Express adapter alongside PondSocket:

```bash
npm install @eleven-am/pondsocket @eleven-am/pondsocket-express
```

## Overview

This package exposes a function that takes your Express app and PondSocket options, and returns the app with additional methods for real-time socket communication:

- `createEndpoint(path, handler)`: Define a WebSocket endpoint on your Express app.
- `listen(...)`: Start the HTTP server and WebSocket server together.

## Server-side Usage

```typescript
import express from "express";
import pondSocket from "@eleven-am/pondsocket-express";

const app = express();

const pondApp = pondSocket(app, {
  // PondSocket options (except 'server')
});

// Create a WebSocket endpoint
const endpoint = pondApp.createEndpoint('/api/socket', (ctx, next) => {
  const token = ctx.request.query.token;
  if (isValidToken(token)) {
    ctx.accept({ role: getRoleFromToken(token) });
  } else {
    ctx.reject('Invalid token', 401);
  }
});

// Create a channel
const channel = endpoint.createChannel('/channel/:id', (ctx, next) => {
  const { role } = ctx.user.assigns;
  const { username } = ctx.joinParams;
  if (role === 'admin') {
    ctx.accept({ username }).trackPresence({
      username,
      role,
      status: 'online',
      onlineSince: Date.now(),
    });
  } else {
    ctx.decline('Insufficient permissions', 403);
  }
});

// Start the server
pondApp.listen(3000);
```

## Client-side Usage

The client API is unchanged—use `@eleven-am/pondsocket-client` as described in the main PondSocket documentation.

```typescript
import PondClient from "@eleven-am/pondsocket-client";

const socket = new PondClient('ws://your-server/api/socket', {
  token: 'your-auth-token'
});
socket.connect();

const channel = socket.createChannel('/channel/123', { username: 'user123' });
channel.join();
channel.broadcast('message', { text: 'Hello, PondSocket!' });
```

## Key Features

- **Express Integration**: Add real-time endpoints to your existing Express app.
- **Simple and Efficient API**: Easy-to-use, type-safe API for WebSocket communication.
- **Organized Channels**: Group users and manage communication efficiently.
- **Assigns**: Store private information for users and channels.
- **Presence**: Track and broadcast user state changes.
- **Broadcasting**: Send messages to all or specific users.
- **Middleware Support**: Extensible request processing.
- **Distributed Support**: Built-in distributed deployment with state sync.

## Advanced Features

### Presence Management

```typescript
// Server-side
channel.onEvent('presence', (ctx, next) => {
  ctx.trackPresence({
    username: ctx.user.assigns.username,
    status: 'online',
    lastSeen: Date.now()
  });
});

// Client-side
channel.onPresence((presences) => {
  console.log('Current users:', presences);
});
```

### User Assigns

```typescript
// Server-side
channel.onEvent('update-profile', (ctx, next) => {
  ctx.assign({
    ...ctx.user.assigns,
    profile: ctx.event.payload
  });
});

// Client-side
channel.onAssigns((assigns) => {
  console.log('User data updated:', assigns);
});
```

### Error Handling

```typescript
// Server-side
channel.onEvent('message', (ctx, next) => {
  try {
    ctx.accept();
  } catch (error) {
    ctx.decline(error.message, 400);
  }
});

// Client-side
channel.onError((error) => {
  console.error('Channel error:', error);
});
```

## Distributed Deployment

PondSocket supports distributed deployment through its distributed backend system. This allows you to scale your application across multiple nodes while maintaining state synchronization.

```typescript
import express from "express";
import pondSocket from "@eleven-am/pondsocket-express";
import { RedisBackend } from "@eleven-am/pondsocket";

const app = express();

const pondApp = pondSocket(app, {
  backend: new RedisBackend({
    host: 'localhost',
    port: 6379
  }),
  // ...other PondSocket options
});

// Now use pondApp.createEndpoint(...) as usual
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the GPL-3.0 License - see the LICENSE file for details.
