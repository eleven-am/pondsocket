# PondSocket Go Server

A high-performance, concurrent WebSocket server implementation in Go that provides the same feature set as the Node.js PondSocket framework. This package enables real-time, bidirectional communication with channel-based messaging, presence tracking, and distributed deployment capabilities.

## Features

- **High-Performance WebSocket Server**: Built with Gorilla WebSocket for optimal performance
- **Channel-Based Messaging**: Organize users into channels with path parameters (`/chat/:roomId`)
- **Presence Tracking**: Real-time user status and state management
- **User Assigns**: Server-side metadata storage and synchronization
- **Broadcasting**: Multiple message targeting options (all users, specific users, all except sender)
- **Middleware Support**: Extensible request/response processing pipeline
- **Distributed Deployment**: Multi-node scaling with PubSub backend support
- **Graceful Shutdown**: Proper resource cleanup and connection handling
- **Type Safety**: Comprehensive error handling and type definitions
- **Context-Based Lifecycle**: Proper cancellation and timeout handling

## Installation

```bash
go get github.com/eleven-am/pondsocket/pkg/go-server
```

## Quick Start

### Basic Server Setup

```go
package main

import (
    "log"
    "time"
    pondsocket "github.com/eleven-am/pondsocket/pkg/go-server"
)

func main() {
    // Create server with default options
    server := pondsocket.NewServer(&pondsocket.ServerOptions{
        ServerAddr: ":8080",
        Options:    pondsocket.DefaultOptions(),
    })

    // Create a WebSocket endpoint
    endpoint := server.CreateEndpoint("/api/socket", handleConnection)

    // Create a channel for chat rooms
    lobby := endpoint.CreateChannel("/chat/:roomId", handleJoin)

    // Handle message events
    lobby.OnEvent("message", handleMessage)

    // Start the server
    log.Println("Starting PondSocket server on :8080")
    if err := server.Listen(); err != nil {
        log.Fatal("Server failed:", err)
    }
}

func handleConnection(ctx *pondsocket.ConnectionContext) error {
    // Access query parameters safely
    token := ctx.Route.Query.Get("token")
    
    // Alternative: access from headers
    if token == "" {
        authHeader := ctx.Headers().Get("Authorization")
        if strings.HasPrefix(authHeader, "Bearer ") {
            token = strings.TrimPrefix(authHeader, "Bearer ")
        }
    }
    
    if isValidToken(token) {
        userInfo := getUserFromToken(token)
        // Set assigns using the proper method
        return ctx.SetAssigns("userId", userInfo.ID).
                  SetAssigns("role", userInfo.Role).
                  SetAssigns("username", userInfo.Username).
                  Accept()
    }
    
    return ctx.Decline(401, "Invalid token")
}

func handleJoin(ctx *pondsocket.JoinContext) error {
    // Access user assigns safely
    userRole := ctx.GetAssigns("role")
    if userRole == nil {
        return ctx.Decline(401, "No role assigned")
    }
    
    // Access route parameters safely
    roomId := ctx.Route.Params["roomId"]
    
    // Parse join payload safely
    var joinParams struct {
        Username string `json:"username"`
    }
    if err := ctx.ParsePayload(&joinParams); err != nil {
        return ctx.Decline(400, "Invalid join parameters")
    }

    if userRole.(string) == "user" {
        return ctx.Accept().
                  SetAssigns("username", joinParams.Username).
                  Track(map[string]interface{}{
                      "username": joinParams.Username,
                      "status":   "online",
                      "joinedAt": time.Now().Unix(),
                  }).
                  Reply("history", map[string]interface{}{
                      "messages": getChannelHistory(roomId),
                  }).
                  Err()
    }
    
    return ctx.Decline(403, "Unauthorized")
}

func handleMessage(ctx *pondsocket.EventContext) error {
    // Parse message payload safely
    var message struct {
        Text string `json:"text"`
    }
    if err := ctx.ParsePayload(&message); err != nil {
        return ctx.Reply("error", map[string]interface{}{
            "message": "Invalid message format",
        }).Err()
    }
    
    // Get user assigns safely
    username, err := ctx.GetAssign("username")
    if err != nil {
        return ctx.Reply("error", map[string]interface{}{
            "message": "Username not found",
        }).Err()
    }

    // Broadcast message to all users in the channel
    return ctx.Broadcast("message", map[string]interface{}{
        "text":      message.Text,
        "username":  username,
        "timestamp": time.Now().Unix(),
    }).Err()
}
```

## Core Concepts

### Server

The main server instance that manages HTTP/WebSocket connections:

```go
server := pondsocket.NewServer(&pondsocket.ServerOptions{
    ServerAddr:         ":8080",
    ServerReadTimeout:  30 * time.Second,
    ServerWriteTimeout: 30 * time.Second,
    Options:           pondsocket.DefaultOptions(),
})
```

### Endpoints

WebSocket gateways that handle client connections:

```go
endpoint := server.CreateEndpoint("/api/socket", func(ctx *pondsocket.ConnectionContext) error {
    // Handle authentication and connection setup
    return ctx.Accept(map[string]interface{}{
        "userId": "user123",
    })
})
```

### Channels

Communication groups where users interact:

```go
lobby := endpoint.CreateChannel("/chat/:roomId", func(ctx *pondsocket.JoinContext) error {
    roomId := ctx.Event().Params["roomId"]
    
    return ctx.Accept(map[string]interface{}{
        "room": roomId,
    }).TrackPresence(map[string]interface{}{
        "status": "online",
    })
})
```

### Event Handling

Process incoming messages from clients:

```go
lobby.OnEvent("message", func(ctx *pondsocket.EventContext) error {
    payload := ctx.Event().Payload.(map[string]interface{})
    
    // Broadcast to all users in channel
    return ctx.Broadcast("message", payload)
})

lobby.OnEvent("typing", func(ctx *pondsocket.EventContext) error {
    // Broadcast to all except sender
    return ctx.BroadcastFrom("typing", map[string]interface{}{
        "userId": ctx.User().UserID,
        "typing": true,
    })
})
```

## Advanced Features

### Presence Management

Track and synchronize user presence across the application:

```go
// Track user presence
ctx.TrackPresence(map[string]interface{}{
    "username": "john_doe",
    "status":   "online",
    "lastSeen": time.Now().Unix(),
})

// Update presence
channel.UpdatePresence(userID, map[string]interface{}{
    "status": "away",
})

// Get all presence data
presenceData := channel.GetPresence()

// Get distributed presence (across all nodes)
distributedPresence := channel.GetDistributedPresence()
```

### User Assigns

Manage server-side metadata for users:

```go
// Update user assigns
channel.UpdateAssigns(userID, "score", 100)

// Get user information
user, err := channel.GetUser(userID)
if err == nil {
    score := user.Assigns["score"].(int)
}
```

### Broadcasting Options

```go
// Broadcast to all users in channel
channel.Broadcast("announcement", map[string]interface{}{
    "message": "Welcome to the chat!",
})

// Broadcast to specific users
channel.BroadcastTo("private_message", map[string]interface{}{
    "text": "Hello there!",
}, "user1", "user2")

// Broadcast to all except sender
channel.BroadcastFrom("user_action", map[string]interface{}{
    "action": "is typing...",
}, senderUserID)
```

### Distributed Deployment

Enable multi-node scaling with PubSub backend:

```go
// Using Redis for distributed deployment
options := pondsocket.DefaultOptions()
options.PubSub = pondsocket.NewRedisPubSub(&pondsocket.RedisPubSubOptions{
    Host:     "localhost",
    Port:     6379,
    Password: "",
    DB:       0,
})

server := pondsocket.NewServer(&pondsocket.ServerOptions{
    ServerAddr: ":8080",
    Options:    options,
})
```

### Middleware

Add request/response processing middleware:

```go
// Add logging middleware
lobby.Use(func(ctx context.Context, req *pondsocket.MessageEvent, res *pondsocket.Channel, next pondsocket.NextFunc) error {
    log.Printf("Message from user %s: %s", req.User.UserID, req.Event.Event)
    return next()
})

// Add rate limiting middleware
lobby.Use(func(ctx context.Context, req *pondsocket.MessageEvent, res *pondsocket.Channel, next pondsocket.NextFunc) error {
    if isRateLimited(req.User.UserID) {
        return pondsocket.TooManyRequests("Rate limit exceeded")
    }
    return next()
})
```

### Best Practices & Safe API Usage

#### Safe Data Access Patterns

```go
// ✅ GOOD: Safe query parameter access
token := ctx.Route.Query.Get("token")

// ✅ GOOD: Safe header access  
authHeader := ctx.Headers().Get("Authorization")

// ✅ GOOD: Safe route parameter access
roomId := ctx.Route.Params["roomId"]

// ✅ GOOD: Safe assigns access with nil checking
userRole := ctx.GetAssigns("role")
if userRole == nil {
    return ctx.Decline(401, "Role not found")
}

// ✅ GOOD: Safe payload parsing with struct validation
var payload struct {
    Text string `json:"text"`
    Type string `json:"type,omitempty"`
}
if err := ctx.ParsePayload(&payload); err != nil {
    return ctx.Reply("error", map[string]interface{}{
        "message": "Invalid payload format",
    }).Err()
}

// ✅ GOOD: Safe assign retrieval with error handling
username, err := ctx.GetAssign("username")
if err != nil {
    return ctx.Reply("error", map[string]interface{}{
        "message": "Username not found",
    }).Err()
}

// ❌ AVOID: Direct type assertions without checking
// text := ctx.GetPayload().(map[string]interface{})["text"].(string)

// ❌ AVOID: Direct assigns access without validation  
// role := ctx.GetUser().Assigns["role"].(string)

// ❌ AVOID: Direct request access
// token := ctx.Request().URL.Query().Get("token")
```

#### Method Chaining Pattern

```go
// ✅ GOOD: Use method chaining with proper error handling
return ctx.Accept().
          SetAssigns("username", username).
          Track(presenceData).
          Reply("welcome", welcomeMessage).
          Err() // Always call .Err() at the end

// ✅ GOOD: Multiple operations with error checking
ctx.SetAssigns("score", 100).
    SetAssigns("level", 5).
    Update(newPresenceData)

if ctx.Err() != nil {
    return ctx.Err()
}

return ctx.Broadcast("level_up", levelData).Err()
```

### Error Handling

```go
func handleMessage(ctx *pondsocket.EventContext) error {
    // Parse payload safely with struct validation
    var message struct {
        Text string `json:"text"`
        Type string `json:"type,omitempty"`
    }
    if err := ctx.ParsePayload(&message); err != nil {
        return ctx.Reply("error", map[string]interface{}{
            "code":    "INVALID_FORMAT",
            "message": "Invalid message format",
        }).Err()
    }
    
    // Validate message content
    if len(message.Text) == 0 {
        return ctx.Reply("error", map[string]interface{}{
            "code":    "EMPTY_MESSAGE",
            "message": "Message text is required",
        }).Err()
    }
    
    if len(message.Text) > 1000 {
        return ctx.Reply("error", map[string]interface{}{
            "code":    "MESSAGE_TOO_LONG",
            "message": "Message too long (max 1000 characters)",
        }).Err()
    }
    
    // Check for profanity
    if containsProfanity(message.Text) {
        return ctx.Reply("error", map[string]interface{}{
            "code":    "PROFANITY_DETECTED",
            "message": "Profanity not allowed",
        }).Err()
    }
    
    // Get user info safely
    username, err := ctx.GetAssign("username")
    if err != nil {
        return ctx.Reply("error", map[string]interface{}{
            "code":    "USER_NOT_FOUND",
            "message": "User information not available",
        }).Err()
    }
    
    return ctx.Broadcast("message", map[string]interface{}{
        "text":      message.Text,
        "userId":    ctx.GetUser().UserID,
        "username":  username,
        "timestamp": time.Now().Unix(),
        "type":      message.Type,
    }).Err()
}
```

### Graceful Shutdown

```go
func main() {
    server := pondsocket.NewServer(&pondsocket.ServerOptions{
        ServerAddr: ":8080",
    })
    
    // Setup endpoints and channels...
    
    // Start server (blocks until shutdown signal)
    if err := server.Listen(); err != nil {
        log.Fatal("Server error:", err)
    }
    
    // Or start manually with custom shutdown handling
    if err := server.Start(); err != nil {
        log.Fatal("Failed to start server:", err)
    }
    
    // Custom shutdown logic
    c := make(chan os.Signal, 1)
    signal.Notify(c, os.Interrupt, syscall.SIGTERM)
    <-c
    
    // Graceful shutdown with 30 second timeout
    if err := server.Stop(30 * time.Second); err != nil {
        log.Printf("Shutdown error: %v", err)
    }
}
```

## Configuration Options

### Server Options

```go
serverOptions := &pondsocket.ServerOptions{
    ServerAddr:         ":8080",
    ServerReadTimeout:  30 * time.Second,
    ServerWriteTimeout: 30 * time.Second,
    ServerIdleTimeout:  120 * time.Second,
    ServerTLSConfig:    tlsConfig, // Optional TLS configuration
    Options:           customOptions,
}
```

### WebSocket Options

```go
options := &pondsocket.Options{
    CheckOrigin:          true,
    AllowedOrigins:       []string{"https://example.com"},
    ReadBufferSize:       4096,
    WriteBufferSize:      4096,
    MaxMessageSize:       1024 * 1024, // 1MB
    PingInterval:         30 * time.Second,
    PongWait:            60 * time.Second,
    WriteWait:           10 * time.Second,
    EnableCompression:    true,
    MaxConnections:       10000,
    SendChannelBuffer:    512,
    ReceiveChannelBuffer: 512,
    InternalQueueTimeout: 5 * time.Second,
}
```

## Client Integration

Use the PondSocket client libraries to connect:

```javascript
// JavaScript/TypeScript client
import PondClient from "@eleven-am/pondsocket-client";

const socket = new PondClient('ws://localhost:8080/api/socket', {
    token: 'your-auth-token'
});

socket.connect();

const channel = socket.createChannel('/chat/123', {
    username: 'user123'
});

channel.join();
channel.broadcast('message', { text: 'Hello, World!' });
```

## Framework Integration

PondSocket can be integrated with any Go HTTP server or framework by using the Manager's HTTPHandler for WebSocket routes. The Manager provides an `http.HandlerFunc` that can be mounted on any HTTP router.

### Echo Web Framework

Here's an example integration with Echo:

```go
package main

import (
    "context"
    "log"
    "net/http"
    "time"

    "github.com/labstack/echo/v4"
    "github.com/labstack/echo/v4/middleware"
    pondsocket "github.com/eleven-am/pondsocket/pkg/go-server"
)

func main() {
    // Create Echo instance
    e := echo.New()
    
    // Add middleware
    e.Use(middleware.Logger())
    e.Use(middleware.Recover())
    e.Use(middleware.CORS())

    // Regular HTTP routes
    e.GET("/", func(c echo.Context) error {
        return c.JSON(http.StatusOK, map[string]string{
            "message": "Echo server with PondSocket WebSockets",
        })
    })

    // API routes
    api := e.Group("/api")
    api.GET("/users", getUsers)
    api.POST("/users", createUser)

    // Create PondSocket Manager for WebSocket handling
    manager := pondsocket.NewManager(context.Background(), *pondsocket.DefaultOptions())
    
    // Setup WebSocket endpoints using the manager
    setupWebSocketEndpoints(manager)

    // Add WebSocket route to Echo - the manager handles the HTTP upgrade
    e.Any("/ws/*", echo.WrapHandler(manager.HTTPHandler()))

    log.Println("Starting Echo server on :8080")
    log.Println("HTTP API: http://localhost:8080/api")
    log.Println("WebSocket: ws://localhost:8080/ws/api/socket")

    // Start Echo server
    e.Logger.Fatal(e.Start(":8080"))
}

func setupWebSocketEndpoints(manager *pondsocket.Manager) {
    // Create WebSocket endpoint for authentication
    endpoint := manager.CreateEndpoint("/ws/api/socket", func(ctx *pondsocket.ConnectionContext) error {
        token := ctx.Route.Query.Get("token")
        
        if !isValidToken(token) {
            return ctx.Decline(401, "Invalid authentication token")
        }

        userInfo := getUserFromToken(token)
        return ctx.SetAssigns("userId", userInfo.ID).
                  SetAssigns("username", userInfo.Username).
                  SetAssigns("role", userInfo.Role).
                  Accept()
    })

    // Create chat room channels
    chatLobby := endpoint.CreateChannel("/chat/:roomId", func(ctx *pondsocket.JoinContext) error {
        roomId := ctx.Route.Params["roomId"]
        username := ctx.GetAssigns("username")
        
        if username == nil {
            return ctx.Decline(401, "Username not found")
        }

        return ctx.Accept().
                  SetAssigns("roomId", roomId).
                  SetAssigns("joinedAt", time.Now().Unix()).
                  Track(map[string]interface{}{
                      "username": username,
                      "status":   "online",
                      "joinedAt": time.Now().Unix(),
                  }).
                  Err()
    })

    // Handle chat messages
    chatLobby.OnEvent("message", func(ctx *pondsocket.EventContext) error {
        var message struct {
            Text string `json:"text"`
        }
        if err := ctx.ParsePayload(&message); err != nil {
            return ctx.Reply("error", map[string]interface{}{
                "message": "Invalid message format",
            }).Err()
        }
        
        if len(message.Text) == 0 {
            return ctx.Reply("error", map[string]interface{}{
                "message": "Message text is required",
            }).Err()
        }

        username, _ := ctx.GetAssign("username")
        roomId := ctx.Route.Params["roomId"]

        // Broadcast message to all users in the room
        return ctx.Broadcast("message", map[string]interface{}{
            "text":      message.Text,
            "userId":    ctx.GetUser().UserID,
            "username":  username,
            "timestamp": time.Now().Unix(),
            "roomId":    roomId,
        }).Err()
    })
}

// HTTP API handlers
func getUsers(c echo.Context) error {
    users := []map[string]interface{}{
        {"id": "1", "username": "john_doe", "status": "online"},
        {"id": "2", "username": "jane_smith", "status": "away"},
    }
    return c.JSON(http.StatusOK, users)
}

func createUser(c echo.Context) error {
    type CreateUserRequest struct {
        Username string `json:"username"`
        Email    string `json:"email"`
    }
    
    var req CreateUserRequest
    if err := c.Bind(&req); err != nil {
        return echo.NewHTTPError(http.StatusBadRequest, "Invalid request")
    }
    
    return c.JSON(http.StatusCreated, map[string]interface{}{
        "id":       "new_user_id",
        "username": req.Username,
        "email":    req.Email,
    })
}

// Helper functions
func isValidToken(token string) bool {
    return token != "" && len(token) > 10
}

func getUserFromToken(token string) *User {
    return &User{
        ID:       "user123",
        Username: "john_doe",
        Role:     "user",
    }
}

type User struct {
    ID       string
    Username string
    Role     string
}
```

### Key Integration Points

1. **Manager Usage**: Create a PondSocket Manager to handle WebSocket upgrades
2. **Route Separation**: Echo handles HTTP routes, Manager handles WebSocket routes via `manager.HTTPHandler()`
3. **Middleware Independence**: Echo middleware and PondSocket operate independently
4. **Shared Context**: Both can access the same database and business logic

### Gin Web Framework

```go
package main

import (
    "context"
    "github.com/gin-gonic/gin"
    pondsocket "github.com/eleven-am/pondsocket/pkg/go-server"
)

func main() {
    // Create Gin router
    r := gin.Default()
    
    // Regular HTTP routes
    r.GET("/ping", func(c *gin.Context) {
        c.JSON(200, gin.H{"message": "pong"})
    })
    
    // Create PondSocket Manager
    manager := pondsocket.NewManager(context.Background(), *pondsocket.DefaultOptions())
    setupWebSocketEndpoints(manager)
    
    // Mount WebSocket handler
    r.Any("/ws/*path", gin.WrapH(manager.HTTPHandler()))
    
    r.Run(":8080")
}
```

### Gorilla Mux

```go
package main

import (
    "context"
    "net/http"
    "github.com/gorilla/mux"
    pondsocket "github.com/eleven-am/pondsocket/pkg/go-server"
)

func main() {
    // Create Mux router
    r := mux.NewRouter()
    
    // Regular HTTP routes
    r.HandleFunc("/api/users", getUsersHandler).Methods("GET")
    
    // Create PondSocket Manager
    manager := pondsocket.NewManager(context.Background(), *pondsocket.DefaultOptions())
    setupWebSocketEndpoints(manager)
    
    // Mount WebSocket handler
    r.PathPrefix("/ws/").Handler(manager.HTTPHandler())
    
    http.ListenAndServe(":8080", r)
}
```

### Chi Router

```go
package main

import (
    "context"
    "net/http"
    "github.com/go-chi/chi/v5"
    "github.com/go-chi/chi/v5/middleware"
    pondsocket "github.com/eleven-am/pondsocket/pkg/go-server"
)

func main() {
    // Create Chi router
    r := chi.NewRouter()
    
    // Add middleware
    r.Use(middleware.Logger)
    r.Use(middleware.Recoverer)
    
    // Regular HTTP routes
    r.Get("/api/users", getUsersHandler)
    
    // Create PondSocket Manager
    manager := pondsocket.NewManager(context.Background(), *pondsocket.DefaultOptions())
    setupWebSocketEndpoints(manager)
    
    // Mount WebSocket handler
    r.Mount("/ws", manager.HTTPHandler())
    
    http.ListenAndServe(":8080", r)
}
```

### Standard Library (net/http)

```go
package main

import (
    "context"
    "net/http"
    pondsocket "github.com/eleven-am/pondsocket/pkg/go-server"
)

func main() {
    // Create ServeMux
    mux := http.NewServeMux()
    
    // Regular HTTP routes
    mux.HandleFunc("/api/users", getUsersHandler)
    mux.HandleFunc("/health", healthHandler)
    
    // Create PondSocket Manager
    manager := pondsocket.NewManager(context.Background(), *pondsocket.DefaultOptions())
    setupWebSocketEndpoints(manager)
    
    // Mount WebSocket handler
    mux.Handle("/ws/", manager.HTTPHandler())
    
    http.ListenAndServe(":8080", mux)
}
```

### Fiber Framework

```go
package main

import (
    "context"
    "github.com/gofiber/fiber/v2"
    "github.com/gofiber/fiber/v2/middleware/adaptor"
    pondsocket "github.com/eleven-am/pondsocket/pkg/go-server"
)

func main() {
    // Create Fiber app
    app := fiber.New()
    
    // Regular HTTP routes
    app.Get("/api/users", getUsersHandler)
    
    // Create PondSocket Manager
    manager := pondsocket.NewManager(context.Background(), *pondsocket.DefaultOptions())
    setupWebSocketEndpoints(manager)
    
    // Mount WebSocket handler using Fiber's HTTP adaptor
    app.All("/ws/*", adaptor.HTTPHandler(manager.HTTPHandler()))
    
    app.Listen(":8080")
}
```

### Key Integration Points

1. **Universal Compatibility**: The Manager's `HTTPHandler()` returns a standard `http.HandlerFunc`
2. **Framework Agnostic**: Works with any HTTP router that can mount `http.Handler` or `http.HandlerFunc`
3. **Route Separation**: HTTP routes handled by your framework, WebSocket routes handled by PondSocket
4. **Middleware Independence**: Framework middleware and PondSocket operate independently
5. **Shared Context**: Both can access the same database and business logic

### Client Connection Example

```javascript
// Connect to WebSocket (works with any of the above integrations)
const socket = new PondClient('ws://localhost:8080/ws/api/socket', {
    token: 'your-auth-token'
});

socket.connect();

// Join a chat room
const chatRoom = socket.createChannel('/chat/general');
chatRoom.join();

// Send a message
chatRoom.broadcast('message', {
    text: 'Hello from any Go HTTP server + PondSocket!'
});
```

## Performance Considerations

- **Connection Limits**: Configure `MaxConnections` based on your server capacity
- **Buffer Sizes**: Adjust `SendChannelBuffer` and `ReceiveChannelBuffer` for your message volume
- **Message Size**: Set appropriate `MaxMessageSize` limits
- **Timeouts**: Configure `PingInterval`, `PongWait`, and `WriteWait` for your network conditions
- **Distributed Setup**: Use Redis PubSub for multi-node deployments

## Examples

See the `examples/` directory for complete implementation examples:

- Basic chat server
- Distributed chat with Redis
- Authentication and authorization
- Rate limiting and middleware
- Gaming server with presence

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the GPL-3.0 License - see the LICENSE file for details.