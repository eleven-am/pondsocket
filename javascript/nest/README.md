# PondSocket NestJS Integration

This package provides a NestJS integration layer for PondSocket, making it easy to use PondSocket's real-time WebSocket
functionality within NestJS applications.

## Installation

```bash
npm install @eleven-am/pondsocket-nest
```

## Overview

This package integrates PondSocket's powerful WebSocket capabilities with NestJS's architecture and dependency injection
system. It provides decorators and services that make it natural to use WebSocket functionality in a NestJS application
while maintaining all of PondSocket's features.

## Key Features

- **NestJS Integration**: Seamless integration with NestJS's module system and dependency injection
- **Decorator-based API**: Use familiar NestJS-style decorators for WebSocket endpoints and channels
- **Guard Support**: Full integration with NestJS guards for authentication and authorization
- **Pipe Support**: Use NestJS pipes for data transformation and validation
- **Type Safety**: Complete TypeScript support with proper type definitions
- **Distributed Support**: Maintains PondSocket's distributed backend capabilities
- **Automatic Discovery**: Uses NestJS's discovery service to automatically find and manage WebSocket endpoints

## Basic Usage

### Module Setup

```typescript
import {Module} from '@nestjs/common';
import {PondSocketModule} from '@eleven-am/pondsocket-nest';

@Module ({
	imports: [
		PondSocketModule.forRoot ({
			guards: [AuthGuard], // Optional: Global guards
			pipes: [ValidationPipe], // Optional: Global pipes
			isGlobal: true, // Optional: Make the module global
		})
	]
})
export class AppModule {}
```

### Creating WebSocket Endpoints

```typescript
import {Controller} from '@nestjs/common';
import {PondSocketEndpoint, PondSocketConnection, Context} from '@eleven-am/pondsocket-nest';

@Endpoint('/api/socket')
export class SocketController {
	@OnConnection()
	async handleConnection (ctx: Context) {
		const token = ctx.request.query.token;
		
		if (isValidToken (token)) {
			const role = getRoleFromToken (token);
			ctx.accept ({role});
		} else {
			ctx.reject ('Invalid token', 401);
		}
	}
}
```

### Creating Channels

```typescript
import {Controller} from '@nestjs/common';
import {PondSocketChannel, PondSocketJoin, Context} from '@eleven-am/pondsocket-nest';

@Channel('/channel/:id')
export class ChannelController {
	@OnJoin()
	async handleJoin (ctx: Context) {
		const {role} = ctx.user.assigns;
		const {username} = ctx.joinParams;
		const {id} = ctx.event.params;
		
		if (role === 'admin') {
			ctx.accept({username})
				.trackPresence({
					username,
					role,
					status: 'online',
					onlineSince: Date.now (),
				});
		} else {
			ctx.decline('Insufficient permissions', 403);
		}
	}
}
```

### Handling Channel Events

```typescript
import {Controller} from '@nestjs/common';
import {PondSocketEvent, Context} from '@eleven-am/pondsocket-nest';

@Channel('/channel/:id')
export class ChannelController {
	@OnEvent('message')
	async handleMessage (ctx: Context) {
		const {text} = ctx.event.payload;
		
		// Broadcast to all users in the channel
		ctx.broadcast('message', {text});
		
		// Broadcast to specific users
		ctx.broadcastTo(['user1', 'user2'], 'message', {text});
		
		// Broadcast to all except sender
		ctx.broadcastFrom('message', {text});
	}
}
```

## Advanced Features

### Presence Management

```typescript

@Channel ('/channel/:id')
export class ChannelController {
	@OnEvent ('presence')
	async handlePresence (ctx: Context) {
		ctx.trackPresence ({
			username: ctx.user.assigns.username,
			status: 'online',
			lastSeen: Date.now ()
		});
	}
}
```

### User Assigns

```typescript

@Channel ('/channel/:id')
export class ChannelController {
	@OnEvent ('update-profile')
	async handleProfileUpdate (ctx: Context) {
		ctx.assign ({
			...ctx.user.assigns,
			profile: ctx.event.payload
		});
	}
}
```

### Error Handling

```typescript
import {PondChannel} from "@eleven-am/pondsocket/types";
import {ChannelInstance} from "@eleven-am/pondsocke-nest";

@Channel ('/channel/:id')
export class ChannelController {
	@ChannelInstance()
	instancd: PondChannel // Instance of the channel
	
	@OnEvent ('message')
	async handleMessage (ctx: Context) {
		try {
			// Your logic here
			ctx.accept();
		} catch (error) {
			ctx.decline (error.message, 400);
		}
	}
}
```

## Distributed Deployment

The package maintains PondSocket's distributed deployment capabilities:

```typescript
import {Module} from '@nestjs/common';
import {PondSocketModule} from '@eleven-am/pondsocket-nest';
import {RedisBackend} from '@eleven-am/pondsocket';

@Module({
	imports: [
		PondSocketModule.forRoot({
			backend: new RedisBackend({
				host: 'localhost',
				port: 6379
			})
		})
	]
})
export class AppModule {
}
```

### Distributed Mode Features

The distributed mode enables you to scale your WebSocket application across multiple server instances while maintaining
state synchronization. Here are the key features:

1. **State Synchronization**
    - Channel presence is synchronized across all instances
    - User assigns are shared between instances
    - Channel events are broadcasted to all instances

2. **Load Balancing**
    - Multiple server instances can handle WebSocket connections
    - Connections are distributed across available instances
    - Automatic failover if an instance goes down

3. **Backend Options**
   ```typescript
   // Redis Backend (Recommended for production)
   import { RedisBackend } from '@eleven-am/pondsocket';
   
   PondSocketModule.forRoot({
     backend: new RedisBackend({
       host: 'localhost',
       port: 6379,
       password: 'optional-password',
       db: 0,
       keyPrefix: 'pondsocket:', // Optional prefix for Redis keys
     })
   })

   // Memory Backend (For development/testing)
   import { MemoryBackend } from '@eleven-am/pondsocket';
   
   PondSocketModule.forRoot({
     backend: new MemoryBackend()
   })
   ```

4. **Configuration Options**
   ```typescript
   interface DistributedBackendOptions {
     // Redis specific options
     host?: string;
     port?: number;
     password?: string;
     db?: number;
     keyPrefix?: string;
     
     // General options
     reconnectInterval?: number; // Time between reconnection attempts
     maxRetries?: number; // Maximum number of reconnection attempts
     timeout?: number; // Operation timeout in milliseconds
   }
   ```

5. **Error Handling**
   ```typescript
   @Channel('/channel/:id')
   export class ChannelController {
     @OnEvent('message')
     async handleMessage(ctx: Context) {
       try {
         // Your logic here
         ctx.accept();
       } catch (error) {
         // Handle distributed backend errors
         if (error instanceof DistributedBackendError) {
           // Handle specific distributed backend errors
           ctx.decline('Backend error occurred', 500);
         } else {
           ctx.decline(error.message, 400);
         }
       }
     }
   }
   ```

6. **Health Checks**
   ```typescript
   import { PondSocketService } from '@eleven-am/pondsocket-nest';

   @Controller('health')
   export class HealthController {
     constructor(private readonly pondSocketService: PondSocketService) {}

     @Get('websocket')
     async checkWebSocketHealth() {
       const isHealthy = await this.pondSocketService.isHealthy();
       return {
         status: isHealthy ? 'healthy' : 'unhealthy',
         timestamp: new Date().toISOString()
       };
     }
   }
   ```

7. **Best Practices**
    - Use Redis backend in production environments
    - Implement proper error handling for distributed operations
    - Monitor backend connection health
    - Use appropriate Redis configuration for your scale
    - Consider using Redis Cluster for high availability
    - Implement proper logging for distributed operations

8. **Scaling Considerations**
    - Monitor Redis memory usage
    - Implement proper cleanup of stale data
    - Consider using Redis Cluster for larger deployments
    - Implement proper error handling and retry mechanisms
    - Monitor network latency between instances
    - Implement proper logging and monitoring

## Configuration Options

The `PondSocketModule.forRoot()` method accepts the following options:

```typescript
interface PondSocketOptions {
	guards?: any[]; // Global guards
	pipes?: any[]; // Global pipes
	providers?: any[]; // Additional providers
	imports?: any[]; // Additional imports
	exports?: any[]; // Additional exports
	isGlobal?: boolean; // Make the module global
	isExclusiveSocketServer?: boolean; // Use exclusive socket server
	backend?: IDistributedBackend; // Distributed backend
}
```

## Client Usage

The client-side usage remains the same as the core PondSocket package:

```typescript
import PondClient from "@eleven-am/pondsocket-client";

const socket = new PondClient('ws://your-server/api/socket', {
	token: 'your-auth-token'
});

socket.connect ();

const channel = socket.createChannel('/channel/123', {
	username: 'user123'
});

channel.join ();

channel.onMessage((event, message) => {
	console.log (`Received message: ${message.text}`);
});

channel.broadcast('message', {text: 'Hello, PondSocket!'});
```

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the GPL-3.0 License - see the LICENSE file for details.

## Return Type Functionality

The NestJS integration provides a powerful return type system that allows you to declaratively specify actions to be
taken when handling WebSocket events. Instead of using the context object directly, you can return an object with
specific properties to trigger various actions.

### Return Type Interface

```typescript
type NestFuncType<Event extends string, Payload extends PondMessage, Presence extends PondPresence, Assigns extends PondAssigns = PondAssigns> =
	{
		// Send an event to the user
		event?: Event;
		
		// Broadcast to all users in the channel
		broadcast?: Event;
		
		// Broadcast to all users except the sender
		broadcastFrom?: Event;
		
		// Update user assigns
		assigns?: Partial<Assigns>;
		
		// Update user presence
		presence?: Presence;
	}
	& Payload;
```

### Usage Examples

#### Channel Join with Multiple Actions

```typescript

@PondSocketChannel('/chat/:roomId')
export class ChatController {
	@PondSocketJoin()
	async handleJoin (ctx: Context) {
		const {username} = ctx.joinParams;
		
		return {
			// Send welcome message to the joining user
			event: 'welcome',
			message: 'Welcome to the chat!',
			
			// Broadcast join notification to all users
			broadcast: 'user_joined',
			username,
			timestamp: Date.now(),
			
			// Update user's assigns
			assigns: {
				username,
				joinedAt: Date.now(),
				role: 'member'
			},
			
			// Update user's presence
			presence: {
				username,
				status: 'online',
				lastSeen: Date.now()
			}
		};
	}
}
```

#### Message Handling

```typescript

@PondSocketChannel('/chat/:roomId')
export class ChatController {
	@PondSocketEvent('message')
	async handleMessage(ctx: Context) {
		const {text} = ctx.event.payload;
		const {username} = ctx.user.assigns;
		
		return {
			// Broadcast the message to all users
			broadcast: 'message',
			text,
			username,
			timestamp: Date.now(),
			
			// Update user's last message timestamp
			assigns: {
				lastMessageAt: Date.now()
			}
		};
	}
}
```

#### Presence Updates

```typescript

@PondSocketChannel ('/chat/:roomId')
export class ChatController {
	@PondSocketEvent('status')
	async handleStatus(ctx: Context) {
		const {status} = ctx.event.payload;
		
		return {
			// Update user's presence
			presence: {
				status,
				lastSeen: Date.now()
			},
			
			// Notify others about the status change
			broadcastFrom: 'status_change',
			username: ctx.user.assigns.username,
			status,
			timestamp: Date.now()
		};
	}
}
```

### Benefits

1. **Declarative Code**: Actions are clearly specified in the return object, making the code more readable and
   maintainable.

2. **Type Safety**: The return type is fully typed, providing excellent TypeScript support and IDE autocompletion.

3. **Reduced Boilerplate**: No need to call multiple context methods; all actions are specified in a single return
   statement.

4. **Flexible Combinations**: You can combine multiple actions in a single return statement, making it easy to handle
   complex scenarios.

5. **Automatic Handling**: The framework automatically processes the returned object and executes the specified actions
   in the correct order.

## Decorators

The PondSocket NestJS integration provides a comprehensive set of decorators to simplify WebSocket endpoint and channel development. These decorators are organized into several categories:

### Class Decorators

| Decorator | Description |
|-----------|-------------|
| `@Channel(path?: string)` | Marks a class as a WebSocket channel handler for the specified path |
| `@Endpoint(path?: string)` | Registers a class as a WebSocket endpoint at the specified path |

### Property Decorators

| Decorator | Description |
|-----------|-------------|
| `@ChannelInstance(channel?: string)` | Injects the underlying PondSocket channel instance into a property |
| `@EndpointInstance()` | Injects the endpoint instance into a property |

### Method Decorators

| Decorator | Description |
|-----------|-------------|
| `@OnConnectionRequest()` | Marks a method to handle new socket connections (authentication, setup, etc.) |
| `@OnEvent(event?: string)` | Marks a method to handle a specific event in a channel |
| `@OnJoinRequest()` | Marks a method to handle when a user joins a channel |
| `@OnLeave()` | Marks a method to handle when a user leaves a channel |
| `@UseGuards(...guards)` | Attaches guard(s) to a class or method for authorization logic |
| `@UsePipes(...pipes)` | Attaches pipe(s) (validators/transformers) to a class or method |

### Parameter Decorators

These decorators inject request-specific data into handler method parameters:

| Decorator | Description |
|-----------|-------------|
| `@GetChannel()` | Injects the current channel instance |
| `@GetConnectionHeaders()` | Injects the current connection headers |
| `@GetConnectionContext()` | Injects the current connection context |
| `@GetContext()` | Injects the current request context |
| `@GetEventParams()` | Injects the current event parameters |
| `@GetEventPayload()` | Injects the current event payload |
| `@GetEventQuery()` | Injects the current event query |
| `@GetEventContext()` | Injects the current event context |
| `@GetJoinParams()` | Injects the current join parameters |
| `@GetJoinContext()` | Injects the current join context |
| `@GetLeaveEvent()` | Injects the current leave event |
| `@GetUserData()` | Injects the current user data |
| `@GetUserAssigns()` | Injects the current user assigns |
| `@GetUserPresence()` | Injects the current user presence |

### Usage Example

Here's an example showing how to use these decorators together:

```typescript
@Channel('/chat/:roomId')
export class ChatController {
  @ChannelInstance()
  private channel: PondChannel;

  @OnJoinRequest()
  async handleJoin(
    @GetContext() ctx: Context,
    @GetJoinParams() params: any
  ) {
    // Handle join request
  }

  @OnEvent('message')
  async handleMessage(
    @GetEventPayload() payload: any,
    @GetUserData() user: any
  ) {
    // Handle message event
  }

  @OnLeave()
  async handleLeave(
    @GetLeaveEvent() event: any,
    @GetUserPresence() presence: any
  ) {
    // Handle leave event
  }
}
```
