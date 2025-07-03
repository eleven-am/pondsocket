# PondSocket Distributed Package

This package provides distributed PubSub implementations for PondSocket, enabling multi-node deployments.

## Redis PubSub

The `RedisPubSub` implementation uses Redis's publish-subscribe functionality to synchronize state and messages across multiple PondSocket server nodes.

### Installation

```bash
go get github.com/redis/go-redis/v9
```

### Usage

```go
import (
    "context"
    "github.com/redis/go-redis/v9"
    "github.com/pondsocket/pkg/go-server/distributed"
)

// Create Redis client
redisClient := redis.NewClient(&redis.Options{
    Addr:     "localhost:6379",
    Password: "", // no password set
    DB:       0,  // use default DB
})

// Create Redis PubSub adapter
ctx := context.Background()
pubsub, err := distributed.NewRedisPubSub(ctx, redisClient)
if err != nil {
    log.Fatal(err)
}
defer pubsub.Close()

// Configure PondSocket with Redis PubSub
options := pondsocket.DefaultOptions()
options.PubSub = pubsub

server := pondsocket.NewServer(&pondsocket.ServerOptions{
    Options: options,
})
```

### Features

- **Pattern Matching**: Supports wildcard patterns (e.g., `pondsocket:api:chat:.*`)
- **Concurrent Safe**: Thread-safe for concurrent publish/subscribe operations
- **Automatic Reconnection**: Handles Redis connection failures gracefully
- **Efficient Message Delivery**: Uses goroutines for non-blocking message handling

### Topic Format

PondSocket uses a hierarchical topic structure:
```
pondsocket:{endpoint}:{channel}:{event}
```

Examples:
- `pondsocket:api:lobby:presence:join`
- `pondsocket:api:chat-room-123:message`
- `pondsocket:api:game-42:assigns:update`

### Production Considerations

1. **Connection Pooling**: Configure Redis client with appropriate pool settings
   ```go
   redisClient := redis.NewClient(&redis.Options{
       Addr:         "localhost:6379",
       PoolSize:     10,
       MinIdleConns: 5,
       MaxRetries:   3,
   })
   ```

2. **Redis Cluster**: For high availability, use Redis Cluster
   ```go
   redisClient := redis.NewClusterClient(&redis.ClusterOptions{
       Addrs: []string{":7000", ":7001", ":7002"},
   })
   ```

3. **Error Handling**: The implementation recovers from handler panics, but you should add logging in production

4. **Message Size**: Redis has a default limit of 512MB per message. Keep messages reasonably sized.

### Testing

Run tests with a local Redis instance:
```bash
# Start Redis
docker run -d -p 6379:6379 redis:latest

# Run tests
go test ./distributed/...
```

## Future Implementations

The package is designed to support additional PubSub backends:
- NATS
- RabbitMQ
- Apache Kafka
- AWS SNS/SQS
- Google Cloud Pub/Sub

Each implementation will follow the same `PubSub` interface, making it easy to switch between different backends.