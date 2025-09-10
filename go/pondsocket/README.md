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
go get github.com/eleven-am/pondsocket/go/pondsocket
```

## Quick Start

### Type Definitions (Best Practice)

Define your data structures for type safety:

```go
// User information structure
type UserInfo struct {
    ID       string `json:"id"`
    Username string `json:"username"`
    Email    string `json:"email,omitempty"`
    Role     string `json:"role"`
}

// Join request payload
type JoinRequest struct {
    Username string `json:"username"`
    Avatar   string `json:"avatar,omitempty"`
}

// Message payload structures
type MessageRequest struct {
    Text      string   `json:"text"`
    Type      string   `json:"type,omitempty"`
    Mentions  []string `json:"mentions,omitempty"`
    ReplyTo   string   `json:"replyTo,omitempty"`
}

type MessageResponse struct {
    ID        string `json:"id"`
    Text      string `json:"text"`
    Username  string `json:"username"`
    UserID    string `json:"userId"`
    Timestamp int64  `json:"timestamp"`
    Type      string `json:"type,omitempty"`
}

// Presence data structure
type UserPresence struct {
    Status    string `json:"status"` // online, away, busy, offline
    LastSeen  int64  `json:"lastSeen"`
    IsTyping  bool   `json:"isTyping,omitempty"`
    Location  string `json:"location,omitempty"`
}

// User assigns structure
type UserAssigns struct {
    UserID    string `json:"userId"`
    Username  string `json:"username"`
    Role      string `json:"role"`
    Score     int    `json:"score,omitempty"`
    Level     int    `json:"level,omitempty"`
    JoinedAt  int64  `json:"joinedAt"`
}

// Error response structure
type ErrorResponse struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Details string `json:"details,omitempty"`
}
```

### Basic Server Setup

```go
package main

import (
    "log"
    "time"
    "github.com/eleven-am/pondsocket/go/pondsocket"
    "github.com/google/uuid"
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
    
    if !isValidToken(token) {
        return ctx.Decline(401, "Invalid token")
    }
    
    // Get user info from token (returns *UserInfo)
    userInfo := getUserFromToken(token)
    if userInfo == nil {
        return ctx.Decline(401, "User not found")
    }
    
    // Set assigns using proper types - store the entire user info struct
    // This is better than storing individual fields
    userAssigns := UserAssigns{
        UserID:   userInfo.ID,
        Username: userInfo.Username,
        Role:     userInfo.Role,
        JoinedAt: time.Now().Unix(),
    }
    
    // Store the structured data as assigns
    return ctx.SetAssigns("userInfo", userAssigns).
              Accept()
}

func handleJoin(ctx *pondsocket.JoinContext) error {
    // Parse user assigns into structured type
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return ctx.Decline(401, "Invalid user assigns")
    }
    
    // Validate user role
    if userAssigns.Role != "user" && userAssigns.Role != "admin" {
        return ctx.Decline(403, "Unauthorized role")
    }
    
    // Access route parameters safely
    roomId := ctx.Route.Params["roomId"]
    if roomId == "" {
        return ctx.Decline(400, "Room ID is required")
    }
    
    // Parse join payload with proper struct
    var joinRequest JoinRequest
    if err := ctx.ParsePayload(&joinRequest); err != nil {
        return ctx.Decline(400, "Invalid join parameters")
    }
    
    // Validate join request
    if joinRequest.Username == "" {
        joinRequest.Username = userAssigns.Username // Use existing username if not provided
    }
    
    // Create structured presence data
    presence := UserPresence{
        Status:   "online",
        LastSeen: time.Now().Unix(),
        IsTyping: false,
        Location: roomId,
    }
    
    // Get channel history with proper type
    history := getChannelHistory(roomId) // Should return []MessageResponse
    
    // Create welcome message with proper structure
    welcomeMsg := struct {
        Messages []MessageResponse `json:"messages"`
        RoomInfo RoomInfo         `json:"roomInfo"`
    }{
        Messages: history,
        RoomInfo: getRoomInfo(roomId),
    }
    
    // Update assigns with room-specific data
    userAssigns.JoinedAt = time.Now().Unix()
    
    return ctx.Accept().
              SetAssigns("userInfo", userAssigns).
              SetAssigns("currentRoom", roomId).
              Track(presence).
              Reply("welcome", welcomeMsg).
              Err()
}

func handleMessage(ctx *pondsocket.EventContext) error {
    // Parse message payload with proper struct
    var messageReq MessageRequest
    if err := ctx.ParsePayload(&messageReq); err != nil {
        errResp := ErrorResponse{
            Code:    "INVALID_FORMAT",
            Message: "Invalid message format",
            Details: err.Error(),
        }
        return ctx.Reply("error", errResp).Err()
    }
    
    // Validate message content
    if err := validateMessage(messageReq); err != nil {
        errResp := ErrorResponse{
            Code:    "VALIDATION_ERROR",
            Message: err.Error(),
        }
        return ctx.Reply("error", errResp).Err()
    }
    
    // Parse user assigns into structured type
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        errResp := ErrorResponse{
            Code:    "USER_NOT_FOUND",
            Message: "User information not available",
        }
        return ctx.Reply("error", errResp).Err()
    }
    
    // Parse user presence (optional)
    var userPresence UserPresence
    if err := ctx.ParsePresence(&userPresence); err == nil {
        // Update typing status if message is sent
        if userPresence.IsTyping {
            userPresence.IsTyping = false
            ctx.UpdatePresence(userPresence)
        }
    }
    
    // Create structured message response
    messageResp := MessageResponse{
        ID:        uuid.NewString(),
        Text:      messageReq.Text,
        Username:  userAssigns.Username,
        UserID:    userAssigns.UserID,
        Timestamp: time.Now().Unix(),
        Type:      messageReq.Type,
    }
    
    // Store message in history (if applicable)
    if err := storeMessage(ctx.Route.Params["roomId"], messageResp); err != nil {
        log.Printf("Failed to store message: %v", err)
    }
    
    // Broadcast structured message to all users
    return ctx.Broadcast("message", messageResp).Err()
}

// Validation helper
func validateMessage(msg MessageRequest) error {
    if len(msg.Text) == 0 {
        return errors.New("message text is required")
    }
    if len(msg.Text) > 1000 {
        return errors.New("message too long (max 1000 characters)")
    }
    if containsProfanity(msg.Text) {
        return errors.New("inappropriate content detected")
    }
    return nil
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
    // Parse authentication token
    token := ctx.Route.Query.Get("token")
    if !isValidToken(token) {
        return ctx.Decline(401, "Invalid token")
    }
    
    // Get user from token and set structured assigns
    userInfo := getUserFromToken(token)
    userAssigns := UserAssigns{
        UserID:   userInfo.ID,
        Username: userInfo.Username,
        Role:     userInfo.Role,
        JoinedAt: time.Now().Unix(),
    }
    
    // Accept connection with structured data
    return ctx.SetAssigns("userInfo", userAssigns).Accept()
})
```

### Channels

Communication groups where users interact:

```go
lobby := endpoint.CreateChannel("/chat/:roomId", func(ctx *pondsocket.JoinContext) error {
    // Parse user assigns
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return ctx.Decline(401, "Invalid user data")
    }
    
    roomId := ctx.Route.Params["roomId"]
    
    // Create structured presence
    presence := UserPresence{
        Status:   "online",
        LastSeen: time.Now().Unix(),
        Location: roomId,
    }
    
    // Accept with structured data
    return ctx.Accept().
              SetAssigns("currentRoom", roomId).
              Track(presence).
              Err()
})
```

### Event Handling

Process incoming messages from clients with type safety:

```go
// Define event-specific types
type TypingEvent struct {
    IsTyping bool `json:"isTyping"`
}

type TypingNotification struct {
    UserID   string `json:"userId"`
    Username string `json:"username"`
    IsTyping bool   `json:"isTyping"`
}

lobby.OnEvent("message", func(ctx *pondsocket.EventContext) error {
    // Parse payload with proper type
    var messageReq MessageRequest
    if err := ctx.ParsePayload(&messageReq); err != nil {
        return ctx.Reply("error", ErrorResponse{
            Code:    "INVALID_MESSAGE",
            Message: "Invalid message format",
        }).Err()
    }
    
    // Parse user assigns
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return ctx.Reply("error", ErrorResponse{
            Code:    "USER_ERROR",
            Message: "User data not found",
        }).Err()
    }
    
    // Create and broadcast structured response
    messageResp := MessageResponse{
        ID:        uuid.NewString(),
        Text:      messageReq.Text,
        Username:  userAssigns.Username,
        UserID:    userAssigns.UserID,
        Timestamp: time.Now().Unix(),
    }
    
    return ctx.Broadcast("message", messageResp).Err()
})

lobby.OnEvent("typing", func(ctx *pondsocket.EventContext) error {
    // Parse typing event
    var typingEvent TypingEvent
    if err := ctx.ParsePayload(&typingEvent); err != nil {
        return nil // Ignore invalid typing events
    }
    
    // Parse user assigns
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return nil // Ignore if user data missing
    }
    
    // Update presence with typing status
    var presence UserPresence
    if err := ctx.ParsePresence(&presence); err == nil {
        presence.IsTyping = typingEvent.IsTyping
        ctx.UpdatePresence(presence)
    }
    
    // Broadcast typing notification to all except sender
    notification := TypingNotification{
        UserID:   userAssigns.UserID,
        Username: userAssigns.Username,
        IsTyping: typingEvent.IsTyping,
    }
    
    return ctx.BroadcastFrom("typing", notification).Err()
})
```

## Advanced Features

### Presence Management

Track and synchronize user presence with structured data:

```go
// Define presence update type
type PresenceUpdate struct {
    Status   string `json:"status"`
    IsTyping bool   `json:"isTyping,omitempty"`
}

// In join handler - track initial presence
func handleJoin(ctx *pondsocket.JoinContext) error {
    presence := UserPresence{
        Status:   "online",
        LastSeen: time.Now().Unix(),
        IsTyping: false,
    }
    
    return ctx.Accept().
              Track(presence).
              Err()
}

// In event handler - update presence
func handleStatusUpdate(ctx *pondsocket.EventContext) error {
    var statusUpdate PresenceUpdate
    if err := ctx.ParsePayload(&statusUpdate); err != nil {
        return ctx.Reply("error", ErrorResponse{
            Code:    "INVALID_STATUS",
            Message: "Invalid status update",
        }).Err()
    }
    
    // Get current presence
    var currentPresence UserPresence
    if err := ctx.ParsePresence(&currentPresence); err != nil {
        // Initialize if not exists
        currentPresence = UserPresence{
            LastSeen: time.Now().Unix(),
        }
    }
    
    // Update fields
    currentPresence.Status = statusUpdate.Status
    currentPresence.IsTyping = statusUpdate.IsTyping
    currentPresence.LastSeen = time.Now().Unix()
    
    // Update presence
    return ctx.UpdatePresence(currentPresence).Err()
}

// Get all presence data (automatically distributed if PubSub configured)
presenceData := ctx.GetAllPresence() // Returns map[string]UserPresence
```

### User Assigns

Manage server-side metadata with type safety:

```go
// Define game-specific assigns
type GameAssigns struct {
    UserAssigns // Embed base assigns
    Score       int    `json:"score"`
    Level       int    `json:"level"`
    Achievements []string `json:"achievements,omitempty"`
}

// Update user assigns in event handler
func handleScoreUpdate(ctx *pondsocket.EventContext) error {
    // Parse current assigns
    var gameAssigns GameAssigns
    if err := ctx.ParseAssigns(&gameAssigns); err != nil {
        // Initialize if not exists
        var userAssigns UserAssigns
        ctx.ParseAssigns(&userAssigns)
        gameAssigns.UserAssigns = userAssigns
    }
    
    // Update score
    gameAssigns.Score += 10
    if gameAssigns.Score >= 100 {
        gameAssigns.Level++
        gameAssigns.Achievements = append(gameAssigns.Achievements, "level_up")
    }
    
    // Save updated assigns
    ctx.SetAssigns("gameInfo", gameAssigns)
    
    // Notify user of changes
    return ctx.Reply("score_update", struct {
        Score int `json:"score"`
        Level int `json:"level"`
    }{
        Score: gameAssigns.Score,
        Level: gameAssigns.Level,
    }).Err()
}

// Get all assigns data (automatically distributed if PubSub configured)
assignsData := ctx.GetAllAssigns() // Returns map[string]interface{}

// Parse into typed structure
for userID, assigns := range assignsData {
    var gameAssigns GameAssigns
    if data, err := json.Marshal(assigns); err == nil {
        json.Unmarshal(data, &gameAssigns)
        log.Printf("User %s: Score=%d, Level=%d", userID, gameAssigns.Score, gameAssigns.Level)
    }
}
```

### Broadcasting Options

```go
// Define broadcast message types
type Announcement struct {
    ID        string `json:"id"`
    Title     string `json:"title"`
    Message   string `json:"message"`
    Type      string `json:"type"` // info, warning, error
    Timestamp int64  `json:"timestamp"`
}

type PrivateMessage struct {
    ID       string `json:"id"`
    From     string `json:"from"`
    To       string `json:"to"`
    Text     string `json:"text"`
    ReadAt   *int64 `json:"readAt,omitempty"`
}

type UserAction struct {
    UserID   string `json:"userId"`
    Username string `json:"username"`
    Action   string `json:"action"`
    Details  string `json:"details,omitempty"`
}

// Broadcast announcement to all users in channel
func broadcastAnnouncement(ctx *pondsocket.EventContext, title, message string) error {
    announcement := Announcement{
        ID:        uuid.NewString(),
        Title:     title,
        Message:   message,
        Type:      "info",
        Timestamp: time.Now().Unix(),
    }
    
    return ctx.Broadcast("announcement", announcement).Err()
}

// Send private message to specific users
func sendPrivateMessage(ctx *pondsocket.EventContext, toUserIDs []string, text string) error {
    var sender UserAssigns
    if err := ctx.ParseAssigns(&sender); err != nil {
        return err
    }
    
    privateMsg := PrivateMessage{
        ID:   uuid.NewString(),
        From: sender.UserID,
        Text: text,
    }
    
    // Send to each recipient
    for _, toUserID := range toUserIDs {
        privateMsg.To = toUserID
        if err := ctx.BroadcastTo("private_message", privateMsg, toUserID).Err(); err != nil {
            log.Printf("Failed to send to %s: %v", toUserID, err)
        }
    }
    
    return nil
}

// Broadcast user action to all except sender
func broadcastUserAction(ctx *pondsocket.EventContext, action string) error {
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return err
    }
    
    userAction := UserAction{
        UserID:   userAssigns.UserID,
        Username: userAssigns.Username,
        Action:   action,
    }
    
    return ctx.BroadcastFrom("user_action", userAction).Err()
}
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

// ✅ GOOD: Safe payload parsing with proper struct types
var messageReq MessageRequest
if err := ctx.ParsePayload(&messageReq); err != nil {
    return ctx.Reply("error", ErrorResponse{
        Code:    ErrInvalidFormat,
        Message: "Invalid payload format",
        Details: err.Error(),
    }).Err()
}

// ✅ GOOD: Safe assigns parsing with defined types
var userAssigns UserAssigns
if err := ctx.ParseAssigns(&userAssigns); err != nil {
    return ctx.Reply("error", ErrorResponse{
        Code:    ErrUserNotFound,
        Message: "Invalid user assigns format",
        Details: err.Error(),
    }).Err()
}

// ✅ GOOD: Safe presence parsing with structured types
var userPresence UserPresence
if err := ctx.ParsePresence(&userPresence); err != nil {
    return ctx.Reply("error", ErrorResponse{
        Code:    "PRESENCE_ERROR",
        Message: "Invalid presence format",
        Details: err.Error(),
    }).Err()
}

// ✅ GOOD: Get all presence data (automatically distributed)
allPresence := ctx.GetAllPresence() // Returns map[string]interface{}
// Parse into typed structure
for userID, presenceData := range allPresence {
    var presence UserPresence
    if data, err := json.Marshal(presenceData); err == nil {
        json.Unmarshal(data, &presence)
        // Use typed presence data
    }
}

// ✅ GOOD: Get all assigns data (automatically distributed)
allAssigns := ctx.GetAllAssigns() // Returns map[string]interface{}
// Parse into typed structure
for userID, assignsData := range allAssigns {
    var assigns UserAssigns
    if data, err := json.Marshal(assignsData); err == nil {
        json.Unmarshal(data, &assigns)
        // Use typed assigns data
    }
}

// ✅ GOOD: Safe assign retrieval with proper error handling
usernameRaw, err := ctx.GetAssign("username")
if err != nil {
    return ctx.Reply("error", ErrorResponse{
        Code:    ErrUserNotFound,
        Message: "Username not found",
    }).Err()
}
username := usernameRaw.(string) // Type assert after nil check

// ❌ AVOID: Direct type assertions without checking
// text := ctx.GetPayload().(map[string]interface{})["text"].(string)

// ❌ AVOID: Direct assigns access without validation  
// role := ctx.GetUser().Assigns["role"].(string)

// ❌ AVOID: Direct request access
// token := ctx.Request().URL.Query().Get("token")
```

#### Method Chaining Pattern

```go
// ✅ GOOD: Use method chaining with structured data
func handleJoinWithChaining(ctx *pondsocket.JoinContext) error {
    // Parse request
    var joinReq JoinRequest
    if err := ctx.ParsePayload(&joinReq); err != nil {
        return ctx.Decline(400, "Invalid join request")
    }
    
    // Build user data
    userAssigns := UserAssigns{
        UserID:   uuid.NewString(),
        Username: joinReq.Username,
        Role:     "user",
        JoinedAt: time.Now().Unix(),
    }
    
    presence := UserPresence{
        Status:   "online",
        LastSeen: time.Now().Unix(),
    }
    
    welcomeMsg := struct {
        Message string `json:"message"`
        RoomID  string `json:"roomId"`
    }{
        Message: fmt.Sprintf("Welcome %s!", joinReq.Username),
        RoomID:  ctx.Route.Params["roomId"],
    }
    
    // Chain operations with proper error handling
    return ctx.Accept().
              SetAssigns("userInfo", userAssigns).
              Track(presence).
              Reply("welcome", welcomeMsg).
              Err() // Always call .Err() at the end
}

// ✅ GOOD: Multiple operations with structured data
func handleLevelUp(ctx *pondsocket.EventContext) error {
    var gameAssigns GameAssigns
    if err := ctx.ParseAssigns(&gameAssigns); err != nil {
        return err
    }
    
    // Update game state
    gameAssigns.Score = 100
    gameAssigns.Level = 5
    
    // Update presence to show achievement
    var presence UserPresence
    ctx.ParsePresence(&presence)
    presence.Status = "achieved_level_5"
    
    // Chain updates
    ctx.SetAssigns("gameInfo", gameAssigns).
        UpdatePresence(presence)
    
    if ctx.Err() != nil {
        return ctx.Err()
    }
    
    // Broadcast achievement
    achievement := struct {
        UserID   string `json:"userId"`
        Username string `json:"username"`
        Level    int    `json:"level"`
        Message  string `json:"message"`
    }{
        UserID:   gameAssigns.UserID,
        Username: gameAssigns.Username,
        Level:    gameAssigns.Level,
        Message:  "reached level 5!",
    }
    
    return ctx.Broadcast("achievement", achievement).Err()
}
```

### Error Handling

```go
// Define error codes as constants
const (
    ErrInvalidFormat    = "INVALID_FORMAT"
    ErrEmptyMessage     = "EMPTY_MESSAGE"
    ErrMessageTooLong   = "MESSAGE_TOO_LONG"
    ErrProfanity        = "PROFANITY_DETECTED"
    ErrUserNotFound     = "USER_NOT_FOUND"
    ErrRateLimited      = "RATE_LIMITED"
    ErrUnauthorized     = "UNAUTHORIZED"
)

func handleMessage(ctx *pondsocket.EventContext) error {
    // Parse payload with proper struct
    var messageReq MessageRequest
    if err := ctx.ParsePayload(&messageReq); err != nil {
        return ctx.Reply("error", ErrorResponse{
            Code:    ErrInvalidFormat,
            Message: "Invalid message format",
            Details: err.Error(),
        }).Err()
    }
    
    // Validate message content
    if len(messageReq.Text) == 0 {
        return ctx.Reply("error", ErrorResponse{
            Code:    ErrEmptyMessage,
            Message: "Message text is required",
        }).Err()
    }
    
    if len(messageReq.Text) > 1000 {
        return ctx.Reply("error", ErrorResponse{
            Code:    ErrMessageTooLong,
            Message: fmt.Sprintf("Message too long (%d/1000 characters)", len(messageReq.Text)),
        }).Err()
    }
    
    // Check for profanity
    if containsProfanity(messageReq.Text) {
        return ctx.Reply("error", ErrorResponse{
            Code:    ErrProfanity,
            Message: "Message contains inappropriate content",
        }).Err()
    }
    
    // Parse user assigns with proper struct
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return ctx.Reply("error", ErrorResponse{
            Code:    ErrUserNotFound,
            Message: "User information not available",
            Details: "Failed to parse user assigns",
        }).Err()
    }
    
    // Create structured message response
    messageResp := MessageResponse{
        ID:        uuid.NewString(),
        Text:      messageReq.Text,
        UserID:    userAssigns.UserID,
        Username:  userAssigns.Username,
        Timestamp: time.Now().Unix(),
        Type:      messageReq.Type,
    }
    
    // Broadcast structured message
    return ctx.Broadcast("message", messageResp).Err()
}

// Helper function for consistent error responses
func sendError(ctx *pondsocket.EventContext, code, message string) error {
    return ctx.Reply("error", ErrorResponse{
        Code:    code,
        Message: message,
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
    "github.com/eleven-am/pondsocket/go/pondsocket"
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

// Add these type definitions at the top of your file
type RoomInfo struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description,omitempty"`
    MaxUsers    int      `json:"maxUsers,omitempty"`
    ActiveUsers int      `json:"activeUsers"`
    Tags        []string `json:"tags,omitempty"`
}

func setupWebSocketEndpoints(manager *pondsocket.Manager) {
    // Create WebSocket endpoint for authentication
    endpoint := manager.CreateEndpoint("/ws/api/socket", func(ctx *pondsocket.ConnectionContext) error {
        token := ctx.Route.Query.Get("token")
        
        if !isValidToken(token) {
            return ctx.Decline(401, "Invalid authentication token")
        }
        
        // Get user from token
        userInfo := getUserFromToken(token)
        if userInfo == nil {
            return ctx.Decline(401, "User not found")
        }
        
        // Create structured assigns
        userAssigns := UserAssigns{
            UserID:   userInfo.ID,
            Username: userInfo.Username,
            Role:     userInfo.Role,
            JoinedAt: time.Now().Unix(),
        }
        
        // Accept with structured data
        return ctx.SetAssigns("userInfo", userAssigns).Accept()
    })

    // Create chat room channels
    chatLobby := endpoint.CreateChannel("/chat/:roomId", func(ctx *pondsocket.JoinContext) error {
        // Parse user assigns
        var userAssigns UserAssigns
        if err := ctx.ParseAssigns(&userAssigns); err != nil {
            return ctx.Decline(401, "Invalid user information")
        }
        
        roomId := ctx.Route.Params["roomId"]
        if roomId == "" {
            return ctx.Decline(400, "Room ID is required")
        }
        
        // Parse join request (optional payload)
        var joinReq JoinRequest
        ctx.ParsePayload(&joinReq) // Ignore error as it's optional
        
        // Update assigns with room info
        userAssigns.JoinedAt = time.Now().Unix()
        
        // Create structured presence
        presence := UserPresence{
            Status:   "online",
            LastSeen: time.Now().Unix(),
            Location: roomId,
        }
        
        // Get room history
        history := getChannelHistory(roomId)
        roomInfo := getRoomInfo(roomId)
        
        // Create welcome message
        welcomeMsg := struct {
            Messages []MessageResponse `json:"messages"`
            RoomInfo RoomInfo         `json:"roomInfo"`
        }{
            Messages: history,
            RoomInfo: roomInfo,
        }
        
        return ctx.Accept().
                  SetAssigns("userInfo", userAssigns).
                  SetAssigns("currentRoom", roomId).
                  Track(presence).
                  Reply("welcome", welcomeMsg).
                  Err()
    })

    // Handle chat messages
    chatLobby.OnEvent("message", func(ctx *pondsocket.EventContext) error {
        // Parse message with proper type
        var messageReq MessageRequest
        if err := ctx.ParsePayload(&messageReq); err != nil {
            return ctx.Reply("error", ErrorResponse{
                Code:    "INVALID_FORMAT",
                Message: "Invalid message format",
                Details: err.Error(),
            }).Err()
        }
        
        // Validate message
        if len(messageReq.Text) == 0 {
            return ctx.Reply("error", ErrorResponse{
                Code:    "EMPTY_MESSAGE",
                Message: "Message text is required",
            }).Err()
        }
        
        // Parse user assigns
        var userAssigns UserAssigns
        if err := ctx.ParseAssigns(&userAssigns); err != nil {
            return ctx.Reply("error", ErrorResponse{
                Code:    "USER_ERROR",
                Message: "User information not found",
            }).Err()
        }
        
        roomId := ctx.Route.Params["roomId"]
        
        // Create structured message response
        messageResp := MessageResponse{
            ID:        uuid.NewString(),
            Text:      messageReq.Text,
            UserID:    userAssigns.UserID,
            Username:  userAssigns.Username,
            Timestamp: time.Now().Unix(),
            Type:      messageReq.Type,
        }
        
        // Store message in history
        storeMessage(roomId, messageResp)
        
        // Broadcast structured message to all users in the room
        return ctx.Broadcast("message", messageResp).Err()
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

func getUserFromToken(token string) *UserInfo {
    // In production, validate token and get user from database
    return &UserInfo{
        ID:       "user123",
        Username: "john_doe",
        Email:    "john@example.com",
        Role:     "user",
    }
}

func getChannelHistory(roomId string) []MessageResponse {
    // In production, fetch from database
    return []MessageResponse{
        {
            ID:        "msg1",
            Text:      "Welcome to the chat!",
            Username:  "System",
            UserID:    "system",
            Timestamp: time.Now().Add(-1 * time.Hour).Unix(),
            Type:      "system",
        },
    }
}

func getRoomInfo(roomId string) RoomInfo {
    // In production, fetch from database
    return RoomInfo{
        ID:          roomId,
        Name:        "General Chat",
        Description: "A place for general discussion",
        MaxUsers:    100,
        ActiveUsers: 5,
        Tags:        []string{"general", "chat"},
    }
}

func storeMessage(roomId string, msg MessageResponse) error {
    // In production, store in database
    log.Printf("Storing message in room %s: %+v", roomId, msg)
    return nil
}

func containsProfanity(text string) bool {
    // In production, use a proper profanity filter
    return false
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
    "github.com/eleven-am/pondsocket/go/pondsocket"
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
    "github.com/eleven-am/pondsocket/go/pondsocket"
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
    "github.com/eleven-am/pondsocket/go/pondsocket"
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
    "github.com/eleven-am/pondsocket/go/pondsocket"
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
    "github.com/eleven-am/pondsocket/go/pondsocket"
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

## Complete Type-Safe Example

Here's a comprehensive example demonstrating all best practices with proper type safety:

```go
package main

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "log"
    "time"
    
    "github.com/eleven-am/pondsocket/go/pondsocket"
    "github.com/google/uuid"
)

// ============== Type Definitions ==============

// User types
type UserInfo struct {
    ID       string `json:"id"`
    Username string `json:"username"`
    Email    string `json:"email,omitempty"`
    Role     string `json:"role"`
}

type UserAssigns struct {
    UserID    string `json:"userId"`
    Username  string `json:"username"`
    Role      string `json:"role"`
    Score     int    `json:"score,omitempty"`
    Level     int    `json:"level,omitempty"`
    JoinedAt  int64  `json:"joinedAt"`
}

type UserPresence struct {
    Status    string `json:"status"` // online, away, busy, offline
    LastSeen  int64  `json:"lastSeen"`
    IsTyping  bool   `json:"isTyping,omitempty"`
    Location  string `json:"location,omitempty"`
}

// Request/Response types
type JoinRequest struct {
    Username string `json:"username"`
    Avatar   string `json:"avatar,omitempty"`
}

type MessageRequest struct {
    Text      string   `json:"text"`
    Type      string   `json:"type,omitempty"`
    Mentions  []string `json:"mentions,omitempty"`
    ReplyTo   string   `json:"replyTo,omitempty"`
}

type MessageResponse struct {
    ID        string   `json:"id"`
    Text      string   `json:"text"`
    Username  string   `json:"username"`
    UserID    string   `json:"userId"`
    Timestamp int64    `json:"timestamp"`
    Type      string   `json:"type,omitempty"`
    Mentions  []string `json:"mentions,omitempty"`
}

type ErrorResponse struct {
    Code    string `json:"code"`
    Message string `json:"message"`
    Details string `json:"details,omitempty"`
}

type TypingEvent struct {
    IsTyping bool `json:"isTyping"`
}

type TypingNotification struct {
    UserID   string `json:"userId"`
    Username string `json:"username"`
    IsTyping bool   `json:"isTyping"`
}

// Error codes
const (
    ErrInvalidFormat  = "INVALID_FORMAT"
    ErrUnauthorized   = "UNAUTHORIZED"
    ErrUserNotFound   = "USER_NOT_FOUND"
    ErrRoomNotFound   = "ROOM_NOT_FOUND"
    ErrMessageTooLong = "MESSAGE_TOO_LONG"
    ErrRateLimited    = "RATE_LIMITED"
)

// ============== Main Application ==============

func main() {
    // Create server with proper configuration
    server := pondsocket.NewServer(&pondsocket.ServerOptions{
        ServerAddr:         ":8080",
        ServerReadTimeout:  30 * time.Second,
        ServerWriteTimeout: 30 * time.Second,
        Options:           createOptions(),
    })
    
    // Create endpoint with authentication
    endpoint := server.CreateEndpoint("/api/socket", handleConnection)
    
    // Create channel with authorization
    lobby := endpoint.CreateChannel("/chat/:roomId", handleJoin)
    
    // Register event handlers
    registerEventHandlers(lobby)
    
    // Start server
    log.Println("Starting PondSocket server on :8080")
    if err := server.Listen(); err != nil {
        log.Fatal("Server failed:", err)
    }
}

func createOptions() *pondsocket.Options {
    opts := pondsocket.DefaultOptions()
    opts.MaxMessageSize = 1024 * 1024 // 1MB
    opts.PingInterval = 30 * time.Second
    opts.PongWait = 60 * time.Second
    return opts
}

// ============== Connection Handler ==============

func handleConnection(ctx *pondsocket.ConnectionContext) error {
    // Extract and validate token
    token := extractToken(ctx)
    if token == "" {
        return ctx.Decline(401, "Authentication required")
    }
    
    // Validate token and get user info
    userInfo, err := validateTokenAndGetUser(token)
    if err != nil {
        return ctx.Decline(401, "Invalid or expired token")
    }
    
    // Create structured assigns
    userAssigns := UserAssigns{
        UserID:   userInfo.ID,
        Username: userInfo.Username,
        Role:     userInfo.Role,
        JoinedAt: time.Now().Unix(),
    }
    
    // Accept connection with structured data
    return ctx.SetAssigns("userInfo", userAssigns).Accept()
}

func extractToken(ctx *pondsocket.ConnectionContext) string {
    // Try query parameter first
    if token := ctx.Route.Query.Get("token"); token != "" {
        return token
    }
    
    // Try Authorization header
    if auth := ctx.Headers().Get("Authorization"); auth != "" {
        if len(auth) > 7 && auth[:7] == "Bearer " {
            return auth[7:]
        }
    }
    
    return ""
}

// ============== Join Handler ==============

func handleJoin(ctx *pondsocket.JoinContext) error {
    // Parse and validate user assigns
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return ctx.Decline(401, "User information not found")
    }
    
    // Validate room access
    roomId := ctx.Route.Params["roomId"]
    if !canAccessRoom(userAssigns, roomId) {
        return ctx.Decline(403, "Access denied to this room")
    }
    
    // Parse optional join request
    var joinReq JoinRequest
    ctx.ParsePayload(&joinReq) // Ignore error as payload is optional
    
    // Create presence data
    presence := UserPresence{
        Status:   "online",
        LastSeen: time.Now().Unix(),
        Location: roomId,
    }
    
    // Get room data
    history, roomInfo := getRoomData(roomId)
    
    // Create structured welcome message
    welcomeMsg := struct {
        Messages []MessageResponse `json:"messages"`
        Room     RoomInfo         `json:"room"`
        User     UserAssigns      `json:"user"`
    }{
        Messages: history,
        Room:     roomInfo,
        User:     userAssigns,
    }
    
    // Accept with all data
    return ctx.Accept().
              SetAssigns("userInfo", userAssigns).
              SetAssigns("currentRoom", roomId).
              Track(presence).
              Reply("welcome", welcomeMsg).
              Err()
}

// ============== Event Handlers ==============

func registerEventHandlers(lobby *pondsocket.Channel) {
    lobby.OnEvent("message", handleMessage)
    lobby.OnEvent("typing", handleTyping)
    lobby.OnEvent("status_update", handleStatusUpdate)
    lobby.OnEvent("private_message", handlePrivateMessage)
}

func handleMessage(ctx *pondsocket.EventContext) error {
    // Parse and validate message
    var msgReq MessageRequest
    if err := ctx.ParsePayload(&msgReq); err != nil {
        return sendError(ctx, ErrInvalidFormat, "Invalid message format")
    }
    
    // Validate message content
    if err := validateMessage(msgReq); err != nil {
        return sendError(ctx, ErrMessageTooLong, err.Error())
    }
    
    // Parse user data
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return sendError(ctx, ErrUserNotFound, "User data not found")
    }
    
    // Update typing status if needed
    var presence UserPresence
    if err := ctx.ParsePresence(&presence); err == nil && presence.IsTyping {
        presence.IsTyping = false
        ctx.UpdatePresence(presence)
    }
    
    // Create response
    msgResp := MessageResponse{
        ID:        uuid.NewString(),
        Text:      msgReq.Text,
        Username:  userAssigns.Username,
        UserID:    userAssigns.UserID,
        Timestamp: time.Now().Unix(),
        Type:      msgReq.Type,
        Mentions:  msgReq.Mentions,
    }
    
    // Store and broadcast
    roomId := ctx.Route.Params["roomId"]
    storeMessage(roomId, msgResp)
    
    return ctx.Broadcast("message", msgResp).Err()
}

func handleTyping(ctx *pondsocket.EventContext) error {
    // Parse typing event
    var typingEvent TypingEvent
    if err := ctx.ParsePayload(&typingEvent); err != nil {
        return nil // Silently ignore invalid typing events
    }
    
    // Parse user data
    var userAssigns UserAssigns
    if err := ctx.ParseAssigns(&userAssigns); err != nil {
        return nil
    }
    
    // Update presence
    var presence UserPresence
    if err := ctx.ParsePresence(&presence); err == nil {
        presence.IsTyping = typingEvent.IsTyping
        presence.LastSeen = time.Now().Unix()
        ctx.UpdatePresence(presence)
    }
    
    // Notify others
    notification := TypingNotification{
        UserID:   userAssigns.UserID,
        Username: userAssigns.Username,
        IsTyping: typingEvent.IsTyping,
    }
    
    return ctx.BroadcastFrom("typing", notification).Err()
}

func handleStatusUpdate(ctx *pondsocket.EventContext) error {
    type StatusUpdate struct {
        Status string `json:"status"`
    }
    
    var update StatusUpdate
    if err := ctx.ParsePayload(&update); err != nil {
        return sendError(ctx, ErrInvalidFormat, "Invalid status format")
    }
    
    // Validate status
    validStatuses := []string{"online", "away", "busy", "offline"}
    if !contains(validStatuses, update.Status) {
        return sendError(ctx, ErrInvalidFormat, "Invalid status value")
    }
    
    // Update presence
    var presence UserPresence
    if err := ctx.ParsePresence(&presence); err != nil {
        presence = UserPresence{}
    }
    
    presence.Status = update.Status
    presence.LastSeen = time.Now().Unix()
    
    return ctx.UpdatePresence(presence).Err()
}

func handlePrivateMessage(ctx *pondsocket.EventContext) error {
    type PrivateMessageReq struct {
        To   string `json:"to"`
        Text string `json:"text"`
    }
    
    var req PrivateMessageReq
    if err := ctx.ParsePayload(&req); err != nil {
        return sendError(ctx, ErrInvalidFormat, "Invalid private message format")
    }
    
    // Validate message
    if len(req.Text) == 0 || len(req.Text) > 500 {
        return sendError(ctx, ErrMessageTooLong, "Message must be 1-500 characters")
    }
    
    // Parse sender data
    var sender UserAssigns
    if err := ctx.ParseAssigns(&sender); err != nil {
        return sendError(ctx, ErrUserNotFound, "Sender data not found")
    }
    
    // Create private message
    privateMsg := struct {
        ID        string `json:"id"`
        From      string `json:"from"`
        FromName  string `json:"fromName"`
        Text      string `json:"text"`
        Timestamp int64  `json:"timestamp"`
    }{
        ID:        uuid.NewString(),
        From:      sender.UserID,
        FromName:  sender.Username,
        Text:      req.Text,
        Timestamp: time.Now().Unix(),
    }
    
    // Send to recipient
    return ctx.BroadcastTo("private_message", privateMsg, req.To).Err()
}

// ============== Helper Functions ==============

func sendError(ctx *pondsocket.EventContext, code, message string) error {
    return ctx.Reply("error", ErrorResponse{
        Code:    code,
        Message: message,
    }).Err()
}

func validateMessage(msg MessageRequest) error {
    if len(msg.Text) == 0 {
        return errors.New("message cannot be empty")
    }
    if len(msg.Text) > 1000 {
        return fmt.Errorf("message too long (%d/1000)", len(msg.Text))
    }
    return nil
}

func canAccessRoom(user UserAssigns, roomId string) bool {
    // Implement your authorization logic
    return true
}

func contains(slice []string, item string) bool {
    for _, s := range slice {
        if s == item {
            return true
        }
    }
    return false
}

// ============== Data Access Functions ==============

type RoomInfo struct {
    ID          string   `json:"id"`
    Name        string   `json:"name"`
    Description string   `json:"description"`
    MaxUsers    int      `json:"maxUsers"`
    ActiveUsers int      `json:"activeUsers"`
}

func validateTokenAndGetUser(token string) (*UserInfo, error) {
    // Implement your token validation
    // In production, validate JWT and query database
    if len(token) < 10 {
        return nil, errors.New("invalid token")
    }
    
    return &UserInfo{
        ID:       uuid.NewString(),
        Username: "john_doe",
        Email:    "john@example.com",
        Role:     "user",
    }, nil
}

func getRoomData(roomId string) ([]MessageResponse, RoomInfo) {
    // In production, fetch from database
    history := []MessageResponse{}
    info := RoomInfo{
        ID:          roomId,
        Name:        "General Chat",
        Description: "Public chat room",
        MaxUsers:    100,
        ActiveUsers: 5,
    }
    return history, info
}

func storeMessage(roomId string, msg MessageResponse) {
    // In production, store in database
    log.Printf("Message stored in room %s: %s", roomId, msg.ID)
}
```

## Examples

See the `examples/` directory for complete implementation examples:

- Basic chat server with type safety
- Distributed chat with Redis
- Authentication and authorization
- Rate limiting and middleware
- Gaming server with presence
- Real-time collaboration tool

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

This project is licensed under the GPL-3.0 License - see the LICENSE file for details.
