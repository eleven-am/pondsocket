# PondSocket

PondSocket is a high-performance, minimalist, and bidirectional socket framework designed for Node.js. It provides a seamless way to handle real-time communication between server and client applications, making it an ideal choice for building WebSocket-based projects.

## Installation

To integrate PondSocket into your Node.js project, simply install it via npm:

```bash
npm install @eleven-am/pondsocket-client
```

## Overview

PondSocket simplifies the complexity of handling WebSocket connections by abstracting the communication process into individual requests rather than dealing with intricate callbacks within the connection event. It offers a lightweight yet powerful solution for managing bidirectional communication channels, enabling real-time updates and collaboration between server and client components.

## Server-side Usage

When setting up the server, PondSocket allows you to create multiple endpoints, each serving as a gateway for sockets to connect and communicate. Each endpoint operates independently, ensuring that sockets from one endpoint cannot interact with sockets from another. This isolation enhances security and simplifies resource management.

```javascript
import PondSocket from "@eleven-am/pondsocket";

const pond = new PondSocket();

// Create an endpoint for handling socket connections
const endpoint = pond.createEndpoint('/api/socket', (req, res) => {
    // Handle socket connection and authentication
});

// Start the server
pond.listen(3000);
```

Within each endpoint, sockets interact through channels. Channels provide an organized way to group users and manage efficient communication among them. When users join a channel, they can participate in real-time events and exchange information with other users in the same channel.

```javascript
const channel = endpoint.createChannel('/channel/:id', (req, res) => {
    // Handle the join request, which is sent when a user attempts to join the channel
});
```

## Client-side Usage

On the client-side, PondSocket provides the PondClient class to establish connections with the server. Clients can easily initiate connections, join channels, and participate in real-time interactions.

```javascript
import PondClient from "@eleven-am/pondsocket-client";

const socket = new PondClient('/api/socket', {});
socket.connect();
```

Once connected, clients can create and join channels to engage in real-time communication with other users and the server.

```javascript
const channel = socket.createChannel('/channel/123');
channel.join();
```

### Node Client

PondSocket also offers a Node.js client, which can be imported using:

```javascript
import PondClient from "@eleven-am/pondsocket-client";
```

This node client allows you to turn another server into a client, enabling easy communication between different server instances.

## Key Features

- **Simple and Efficient API**: PondSocket offers an easy-to-use API, making WebSocket communication straightforward and hassle-free.
- **Organized Channels**: Channels provide a structured approach for grouping users and facilitating efficient communication.
- **Assigns**: PondSocket allows the storage of private information for users and channels, enhancing data security.
- **Presence**: The presence feature keeps track of users' current states and notifies other users about any changes.
- **Broadcasting**: PondSocket enables broadcasting messages to all users or specific groups within a channel, facilitating real-time updates.
- **Typed and Well-documented**: The codebase is thoroughly documented and typed, providing a seamless development experience with improved IDE suggestions.

## Examples

### Client-side Example with Authentication

To connect to the PondSocket server and send messages while associating a username with the client connection, follow the steps below:

```javascript
import PondClient from "@eleven-am/pondsocket-client";

// Your server URL
const serverUrl = 'ws://your-server-url/api/socket';

// Your authenticated user's token (replace with actual token)
const authToken = 'your-auth-token';

// Your username (replace with actual username)
const username = 'user123';

// Create a new PondClient instance
const socket = new PondClient(serverUrl, { token: authToken });

// Connect to the server
socket.connect();

// Add event listeners to handle various scenarios
socket.onConnectionChange((connected) => {
    if (connected) {
        console.log('Connected to the server.');
    } else {
        console.log('Disconnected from the server.');
    }
});

// Create a channel and join it
const channel = socket.createChannel('/channel/123', { username });
channel.join();

// Send a message to the server
const message = "Hello, PondSocket!";
channel.broadcast('message', { text: message });

// Handle received messages
// Certain methods in the channel instance returns a subscription function, which can be used to unsubscribe from the event
const subscription = channel.onMessage((event, message) => {
    console.log(`Received message from server: ${message.text}`);
});

// Unsubscribe from the event
subscription();
```

The client will now connect to the server, and the server will receive the necessary headers automatically, including any authentication tokens or cookies, as required by the browser.

### Server-side Example with Authentication and check for profanity before broadcasting

To create a PondSocket server that accepts authenticated connections and checks for profanity before broadcasting messages, follow the steps below:

```javascript
import PondSocket from "@eleven-am/pondsocket";

// Helper functions for token validation
function isValidToken(token) {
    // Implement your token validation logic here
    // Return true if the token is valid, false otherwise
    return true;
}

function getRoleFromToken(token) {
    // Implement the logic to extract the user's role from the token
    // Return the user's role
    return 'user';
}

function isTextProfane(text) {
    // Implement your profanity check logic here
    // Return true if the text is profane, false otherwise
    return false;
}

function getMessagesFromDatabase(channelId) {
    // Implement your logic to retrieve messages from the database
    // Return an array of messages
    return [];
}

const pond = new PondSocket();

// Create an endpoint for handling socket connections
const endpoint = pond.createEndpoint('/api/socket', (req, res) => {
    // Depending if the user already has cookies set, they can be accessed from the request headers or the request address
    const token = req.query.token; // If the token is passed as a query parameter

    // Perform token validation here
    if (isValidToken(token)) {
        // Extract the authenticated user's username
        const role = getRoleFromToken(token);

        // Handle socket connection and authentication for valid users
        res.accept({role}); // Assign the user's role to the socket
    } else {
        // Reject the connection for invalid users or without a token
        res.reject('Invalid token', 401);
    }
});

// Create a channel, providing a callback that is called when a user attempts to join the channel
const profanityChannel = endpoint.createChannel('/channel/:id', async (req, res) => {
    // When joining the channel, any joinParams passed from the client will be available in the request payload
    // Also any previous assigns on the socket will be available in the request payload as well
    const {role} = req.user.assigns;
    const {username} = req.joinParams;
    const {id} = req.event.params;

    // maybe retrieve the previous messages from the database
    const messages = await getMessagesFromDatabase(id);

    // Check if the user has the required role to join the channel
    if (role === 'admin') {
        // Accept the join request
        res.accept({username, profanityCount: 0})
            // optionally you can track the presence of the user in the channel
            .trackPresence({
                username,
                role,
                status: 'online',
                onlineSince: Date.now(),
            })
            // and send the user the channel history
            .sendToUsers('history', {messages}, [req.user.id]);

        // Alternatively, you can also send messages to the user, NOTE that the user would be automatically subscribed to the channel.
        // res.send('history', { messages }, { username, profanityCount: 0 })
        //   .trackPresence({
        //       username,
        //       role,
        //       status: 'online',
        //       onlineSince: Date.now(),
        //   });
    } else {
        // Reject the join request
        res.decline('You do not have the required role to join this channel', 403);
    }
});

// Attach message event listener to the profanityChannel
profanityChannel.onEvent('message', (req, res) => {
    const {text} = req.event.payload;

    // Check for profanity
    if (isTextProfane(text)) {
        // Reject the message if it contains profanity
        res.decline('Profanity is not allowed', 400, {
            profanityCount: req.user.assigns.profanityCount + 1
        });

        // note that profanityCount is updated so req.user.assigns.profanityCount will be updated
        if (req.user.assigns.profanityCount >= 3) {
            // Kick the user from the channel if they have used profanity more than 3 times
            res.evictUser('You have been kicked from the channel for using profanity');
        } else {
            // you can broadcast a message to all users or In the channel that profanity is not allowed
            res.broadcast('profanity-warning', {message: 'Profanity is not allowed'})
                // or you can send a message to the user that profanity is not allowed
                .sendToUsers('profanity-warning', {message: `You have used profanity ${profanityCount} times. You will be kicked from the channel if you use profanity more than 3 times.`}, [req.user.id]);
        }
    } else {
        // Accept the message to allow broadcasting to other clients in the channel
        res.accept();
    }

    // for more complete access to the channel, you can use the channel instance
    // const channel = req.channel;
});

profanityChannel.onEvent('presence/:presence', (req, res) => {
    const {presence} = req.event.params;
    const {username} = req.user.assigns;

    // Handle presence events
    res.updatePresence({
        username,
        role,
        onlineSince: Date.now(),
        status: presence,
    });
});

profanityChannel.onLeave((event) => {
    const {username} = event.assigns;

    // When a user leaves the channel, PondSocket will automatically remove the user from the presence list and inform other users in the channel

    // perform a cleanup operation here
});

// Start the server
pond.listen(3000, () => {
    console.log('PondSocket server listening on port 3000');
});
```

## API Documentation

### PondSocket

The `PondSocket` class is the core class that represents the socket server.

**Constructor:**

- `constructor(server?: HTTPServer, socketServer?: WebSocketServer)`: Creates a new instance of the PondSocket with an optional HTTP server and WebSocket server.

**Methods:**

- `listen(...args: any[]): HTTPServer`: Specifies the port to listen on with the provided arguments.

- `close(callback?: () => void): HTTPServer`: Closes the server, and an optional callback can be provided.

- `createEndpoint<Path extends string>(path: PondPath<Path>, handler: (request: IncomingConnection<Path>, response: ConnectionResponse) => void | Promise<void>): Endpoint`: Accepts a new socket upgrade request on the provided endpoint using the handler function to authenticate the socket.

### ConnectionResponse

The `ConnectionResponse` class represents the response object for the incoming connection.

**Methods:**

- `accept(assigns?: PondAssigns): void`: Accepts the request and optionally assigns data to the client.

- `reject(message?: string, errorCode?: number): void`: Rejects the request with the given error message and optional error code.

- `send(event: string, payload: PondMessage, assigns?: PondAssigns): void`: Emits a direct message to the client with the specified event and payload.

### Endpoint

The `Endpoint` class represents an endpoint in the PondSocket server where channels can be created.

**Methods:**

- `createChannel<Path extends string>(path: PondPath<Path>, handler: (request: JoinRequest<Path>, response: JoinResponse) => void | Promise<void>): PondChannel`: Adds a new PondChannel to this path on this endpoint with the provided handler function to authenticate the client.

- `broadcast(event: string, payload: PondMessage): void`: Broadcasts a message to all clients connected to this endpoint with the specified event and payload.

- `closeConnection(clientIds: string | string[]): void`: Closes specific clients connected to this endpoint identified by the provided clientIds.

### JoinRequest

The `JoinRequest` class represents the request object when a client joins a channel.

**Properties:**

- `event: PondEvent<Path>`: The event associated with the request.

- `channelName: string`: The name of the channel.

- `assigns: UserAssigns`: The assigns data for the client.

- `presence: UserPresences`: The presence data for the client.

- `joinParams: JoinParams`: The join parameters for the client.

- `user: UserData`: The user data associated with the client.

- `channel: Channel`: The Channel instance associated with the request.

### JoinResponse

The `JoinResponse` class represents the response object for the join request.

**Methods:**

- `accept(assigns?: PondAssigns): JoinResponse`: Accepts the join request and optionally assigns data to the client.

- `reject(message?: string, errorCode?: number): JoinResponse`: Rejects the join request with the given error message and optional error code.

- `send(event: string, payload: PondMessage, assigns?: PondAssigns): JoinResponse`: Emits a direct message to the client with the specified event, payload, and optional assigns data.

- `broadcast(event: string, payload: PondMessage): JoinResponse`: Emits a message to all clients in the channel with the specified event and payload.

- `broadcastFromUser(event: string, payload: PondMessage): JoinResponse`: Emits a message to all clients in the channel except the sender with the specified event and payload.

- `sendToUsers(event: string, payload: PondMessage, userIds: string[]): JoinResponse`: Emits a message to a specific set of clients identified by the provided userIds with the specified event and payload.

- `trackPresence(presence: PondPresence): JoinResponse`: Tracks the presence of the client in the channel.

### PondChannel

The `PondChannel` class represents a Generic channel in the PondSocket server. It is used to create a channel whose path matches the provided PondPath.

**Methods:**

- `onEvent<Event extends string>(event: PondPath<Event>, handler: (request: EventRequest<Event>, response: EventResponse) => void | Promise<void>): void`: Handles an event request made by a user for the specified event with the provided handler function.

- `broadcast(event: string, payload: PondMessage, channelName?: string): void`: Broadcasts a message to all users in the channel with the specified event and payload. Optionally, a specific channel name can be provided to broadcast the message only to users in that channel.

- `onLeave(handler: (event: LeaveEvent) => void | Promise<void>): void`: Handles a leave event for the channel with the provided handler function when a user leaves the channel.

### EventRequest

The `EventRequest` class represents the request object when an event is received from a client.

**Properties:**

- `event: PondEvent<Path>`: The event associated with the request.

- `channelName: string`: The name of the channel.

- `assigns: UserAssigns`: The assigns data for the client.

- `presence: UserPresences`: The presence data for the client.

- `user: UserData`: The user data associated with the client.

- `channel: Channel`: The Channel instance associated with the request.

### EventResponse

The `EventResponse` class represents the response object for handling events from clients.

**Methods:**

- `accept(assigns?: PondAssigns): EventResponse`: Accepts the request and optionally assigns data to the client.

- `reject(message?: string, errorCode?: number, assigns?: PondAssigns): EventResponse`: Rejects the request with the given error message, optional error code, and optional assigns data.

- `send(event: string, payload: PondMessage, assigns?: PondAssigns): void`: Emits a direct message to the client with the specified event, payload, and optional assigns data.

- `broadcast(event: string, payload: PondMessage): EventResponse`: Sends a message to all clients in the channel with the specified event and payload.

- `broadcastFromUser(event: string, payload: PondMessage): EventResponse`: Sends a message to all clients in the channel except the sender with the specified event and payload.

- `sendToUsers(event: string, payload: PondMessage, userIds: string[]): EventResponse`: Sends a message to a specific set of clients identified by the provided userIds with the specified event and payload.

- `trackPresence(presence: PondPresence, userId?: string): EventResponse`: Tracks a user's presence in the channel.

- `updatePresence(presence: PondPresence, userId?: string): EventResponse`: Updates a user's presence in the channel.

- `unTrackPresence(userId?: string): EventResponse`: Removes a user's presence from the channel.

- `evictUser(reason: string, userId?: string): void`: Evicts a user from the channel.

- `closeChannel(reason: string): void`: Closes the channel from the server-side for all clients.

### Channel

The `Channel` class represents a single Channel created by the PondSocket server. Note that a PondChannel can have multiple channels associated with it.

**Methods:**

- `name: string`: The name of the channel.

- `getAssigns: UserAssigns`: Gets the current assign data for the client.

- `getUserData(userId: string): UserData`: Gets the assign data for a specific user identified by the provided `userId`.

- `broadcastMessage(event: string, payload: PondMessage): void`: Broadcasts a message to every client in the channel with the specified event and payload.

- `sendToUser(userId: string, event: string, payload: PondMessage): void`: Sends a message to a specific client in the channel identified by the provided `userId`, with the specified event and payload.

- `sendToUsers(userIdS: string[], event: string, payload: PondMessage): void`: Sends a message to a specific set of clients identified by the provided `userIdS`, with the specified event and payload.

- `evictUser(userId: string, reason?: string): void`: Bans a user from the channel identified by the provided `userId`. Optionally, you can provide a `reason` for the ban.

- `trackPresence(userId: string, presence: PondPresence): void`: Tracks a user's presence in the channel identified by the provided `userId`.

- `removePresence(userId: string): void`: Removes a user's presence from the channel identified by the provided `userId`.

- `updatePresence(userId: string, presence: PondPresence): void`: Updates a user's presence in the channel identified by the provided `userId`.

### PondClient

The `PondClient` class represents a client that connects to the PondSocket server.

**Constructor:**

- `constructor(endpoint: string, params?: Record<string, any>)`: Creates a new instance of the PondClient with the provided endpoint URL and optional parameters.

**Methods:**

- `connect(backoff?: number): void`: Connects to the server with an optional backoff time.

- `getState(): boolean`: Returns the current state of the socket.

- `disconnect(): void`: Disconnects the socket.

- `createChannel(name: string, params?: JoinParams): ClientChannel`: Creates a channel with the given name and optional join parameters.

- `onConnectionChange(callback: (state: boolean) => void): Unsubscribe`: Subscribes to the connection state changes and calls the provided callback when the state changes.

### ClientChannel

The `ClientChannel` class represents a channel in the PondClient.

**Methods:**

- `join(): void`: Connects to the channel.

- `leave(): void`: Disconnects from the channel.

- `onMessage(callback: (event: string, message: PondMessage) => void): Unsubscribe`: Monitors the channel for messages and calls the provided callback when a message is received.

- `onMessageEvent(event: string, callback: (message: PondMessage) => void): Unsubscribe`: Monitors the channel for messages with the specified event and calls the provided callback when a message is received.

- `onChannelStateChange(callback: (connected: ChannelState) => void): Unsubscribe`: Monitors the channel state of the channel and calls the provided callback when the connection state changes.

- `onJoin(callback: (presence: PondPresence) => void): Unsubscribe`: Detects when clients join the channel and calls the provided callback when a client joins the channel.

- `onLeave(callback: (presence: PondPresence) => void): Unsubscribe`: Detects when clients leave the channel and calls the provided callback when a client leaves the channel.

- `onPresenceChange(callback: (presence: PresencePayload) => void): Unsubscribe`: Detects when clients change their presence in the channel and calls the provided callback when a client changes their presence in the channel.

- `sendMessage(event: string, payload: PondMessage, recipient: string[]): void`: Sends a message to specific clients in the channel with the specified event, payload, and recipient.

- `sendForResponse(event: string, payload: PondMessage): Promise<PondMessage>`: Sends a message to the server with the specified event, payload, and returns a promise that resolves with the response.

- `broadcastFrom(event: string, payload: PondMessage): void`: Broadcasts a message to every other client in the channel except yourself with the specified event and payload.

- `broadcast(event: string, payload: PondMessage): void`: Broadcasts a message to the channel, including yourself, with the specified event and payload.

- `getPresence(): PondPresence[]`: Gets the current presence of the channel.

- `onUsersChange(callback: (users: PondPresence[]) => void): Unsubscribe`: Monitors the presence of the channel and calls the provided callback when the presence changes.

- `isConnected(): boolean`: Gets the current connection state of the channel.

- `onConnectionChange(callback: (connected: boolean) => void): Unsubscribe`: Monitors the connection state of the channel and calls the provided callback when the connection state changes.

## License

PondSocket is released under the GPL-3.0 License. Please refer to the `LICENSE` file for detailed licensing information.

## Conclusion

PondSocket is a powerful and versatile solution for building real-time applications that require efficient bidirectional communication between server and client components. Its minimalist design and comprehensive feature set make it an excellent choice for WebSocket-based projects, providing developers with a straightforward and reliable tool for building real-time communication systems. With the Node.js client, it also allows for easy communication between multiple server instances, expanding its capabilities even further.
