# PondSocket

PondSocket is a high-performance, minimalist, and bidirectional socket framework designed for Node.js. It provides a seamless way to handle real-time communication between server and client applications, making it an ideal choice for building WebSocket-based projects.

## Installation

To integrate PondSocket into your Node.js project, simply install it via npm:

```bash
npm install @eleven-am/pondsocket
```

## Overview

PondSocket simplifies the complexity of handling WebSocket connections by abstracting the communication process into individual requests rather than dealing with intricate callbacks within the connection event. It offers a lightweight yet powerful solution for managing bidirectional communication channels, enabling real-time updates and collaboration between server and client components.

## Key Features

- **Simple and Efficient API**: PondSocket offers an easy-to-use API, making WebSocket communication straightforward and hassle-free.
- **Organized Channels**: Channels provide a structured approach for grouping users and facilitating efficient communication.
- **Assigns**: PondSocket allows the storage of private information for users and channels, enhancing data security.
- **Presence**: The presence feature keeps track of users' current states and notifies other users about any changes.
- **Broadcasting**: PondSocket enables broadcasting messages to all users or specific groups within a channel, facilitating real-time updates.
- **Type Safety**: The codebase is thoroughly typed with TypeScript, providing a seamless development experience with improved IDE suggestions.
- **Middleware Support**: Extensible middleware system for request processing and validation.
- **Distributed Support**: Built-in support for distributed deployment with state synchronization.

## Server-side Usage

When setting up the server, PondSocket allows you to create multiple endpoints, each serving as a gateway for sockets to connect and communicate. Each endpoint operates independently, ensuring that sockets from one endpoint cannot interact with sockets from another. This isolation enhances security and simplifies resource management.

```typescript
import PondSocket from "@eleven-am/pondsocket";

const pond = new PondSocket();

// Create an endpoint for handling socket connections
const endpoint = pond.createEndpoint('/api/socket', (ctx, next) => {
    // Handle socket connection and authentication
    const token = ctx.request.query.token;
    
    if (isValidToken(token)) {
        const role = getRoleFromToken(token);
        ctx.accept({ role }); // Assign the user's role to the socket
    } else {
        ctx.reject('Invalid token', 401);
    }
});

// Create a channel with path parameters
const channel = endpoint.createChannel('/channel/:id', (ctx, next) => {
    const { role } = ctx.user.assigns;
    const { username } = ctx.joinParams;
    const { id } = ctx.event.params;

    if (role === 'admin') {
        ctx.accept({ username })
            .trackPresence({
                username,
                role,
                status: 'online',
                onlineSince: Date.now(),
            });
    } else {
        ctx.decline('Insufficient permissions', 403);
    }
});

// Handle channel events
channel.onEvent('message', (ctx, next) => {
    const { text } = ctx.event.payload;
    
    // Broadcast to all users in the channel
    ctx.broadcast('message', { text });
    
    // Or broadcast to specific users
    ctx.broadcastTo(['user1', 'user2'], 'message', { text });
    
    // Or broadcast to all except sender
    ctx.broadcastFrom('message', { text });
});

// Start the server
pond.listen(3000);
```

## Client-side Usage

On the client-side, PondSocket provides the PondClient class to establish connections with the server. Clients can easily initiate connections, join channels, and participate in real-time interactions.

```typescript
import PondClient from "@eleven-am/pondsocket-client";

// Create a new client instance
const socket = new PondClient('ws://your-server/api/socket', {
    token: 'your-auth-token'
});

// Connect to the server
socket.connect();

// Handle connection state changes
socket.onConnectionChange((connected) => {
    if (connected) {
        console.log('Connected to the server');
    } else {
        console.log('Disconnected from the server');
    }
});

// Create and join a channel
const channel = socket.createChannel('/channel/123', {
    username: 'user123'
});

// Join the channel
channel.join();

// Handle channel events
const subscription = channel.onMessage((event, message) => {
    console.log(`Received message: ${message.text}`);
});

// Send a message
channel.broadcast('message', { text: 'Hello, PondSocket!' });

// Leave the channel
channel.leave();

// Unsubscribe from events
subscription();
```

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
        // Your logic here
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
import PondSocket from "@eleven-am/pondsocket";
import { RedisBackend } from "@eleven-am/pondsocket";

const pond = new PondSocket({
    backend: new RedisBackend({
        host: 'localhost',
        port: 6379
    })
});
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the GPL-3.0 License - see the LICENSE file for details.
