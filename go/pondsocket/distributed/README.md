# PondSocket Go Redis adapter

Redis Pub/Sub implementation of the Go server's `pondsocket.PubSub` interface. This subpackage exists because the broker adapter has its own dependency, initialization failure mode, subscription lifecycle, and operational security requirements; the server README only shows how to attach it.

## Use

```go
package main

import (
	"context"
	"log"

	pondsocket "github.com/eleven-am/pondsocket/go/pondsocket"
	"github.com/eleven-am/pondsocket/go/pondsocket/distributed"
	"github.com/redis/go-redis/v9"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	redisClient := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})
	defer redisClient.Close()

	pubsub, err := distributed.NewRedisPubSub(ctx, redisClient)
	if err != nil {
		log.Fatal(err)
	}
	defer pubsub.Close()

	options := pondsocket.DefaultOptions()
	options.PubSub = pubsub
	options.Namespace = "my-app"

	manager := pondsocket.NewManager(ctx, *options)
	_ = manager
}
```

`NewRedisPubSub` pings Redis and returns an error if initialization fails. The caller owns the supplied `redis.Client`; close both the PondSocket adapter and the client during shutdown.

## Subscription behavior

`Subscribe(pattern, handler)` registers an ordered handler. `SubscribeWithCancel` additionally returns an idempotent function that removes only that registration. `Unsubscribe(pattern)` removes every local handler for the pattern. `Close` cancels delivery, closes the Redis subscription, and waits for the receiver goroutine.

PondSocket topic patterns use `.*` as the multi-segment wildcard. The adapter translates them to Redis glob patterns internally. Applications normally should not publish framework messages directly; configure the adapter on `pondsocket.Options` and let the server produce the canonical envelopes.

## Protocol and trust boundary

The normative cross-runtime topics and JSON payloads are defined in [`DISTRIBUTED_PROTOCOL.md`](../../../DISTRIBUTED_PROTOCOL.md). Do not duplicate that contract here.

Redis Pub/Sub does not authenticate publishers or validate application authorization. Use a unique namespace per mutually trusted application, restrict network and credential access, enable TLS where appropriate, and do not expose the broker to untrusted clients.

## Tests

The adapter tests use an in-memory Redis server:

```bash
go test ./distributed
go test -race ./distributed
```

## License

GPL-3.0
