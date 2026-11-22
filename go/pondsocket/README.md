# PondSocket Go server

Channel-based WebSocket and SSE server for Go. The package provides connection and channel authorization, ordered event handling, presence, private assigns, request/reply, middleware, bounded queues, lifecycle hooks, and optional Redis-backed distribution.

## Install

```bash
go get github.com/eleven-am/pondsocket/go/pondsocket
```

The module currently requires Go 1.24 or newer.

## Quick start

```go
package main

import (
	"log"

	pondsocket "github.com/eleven-am/pondsocket/go/pondsocket"
)

func main() {
	server := pondsocket.NewServer(&pondsocket.ServerOptions{
		ServerAddr: ":8080",
		Options:    pondsocket.DefaultOptions(),
	})

	endpoint := server.CreateEndpoint("/v1/socket", func(ctx *pondsocket.ConnectionContext) error {
		if len(ctx.Route.QueryParam("token")) == 0 {
			return ctx.Decline(pondsocket.StatusUnauthorized, "Missing token")
		}

		ctx.SetAssigns("role", "member")
		return ctx.Accept()
	})

	chat := endpoint.CreateChannel("/chat/:roomId", func(ctx *pondsocket.JoinContext) error {
		ctx.Accept().Track(map[string]any{"online": true})
		return nil
	})

	chat.OnMessage("message", func(ctx *pondsocket.EventContext) error {
		ctx.Broadcast("message", ctx.GetPayload())
		return nil
	})

	if err := server.Listen(); err != nil {
		log.Fatal(err)
	}
}
```

`Listen` blocks and handles `SIGINT`/`SIGTERM`. For application-managed lifecycle, use `Start`, `IsRunning`, and `Stop(timeout)`.

## Bring your own HTTP server

Use a `Manager` when another component owns the HTTP server:

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

manager := pondsocket.NewManager(ctx, *pondsocket.DefaultOptions())
manager.CreateEndpoint("/v1/socket", func(ctx *pondsocket.ConnectionContext) error {
	return ctx.Accept()
})

server := &http.Server{
	Addr:              ":8080",
	Handler:           manager.HTTPHandler(),
	ReadHeaderTimeout: 5 * time.Second,
}
```

The same endpoint supports WebSocket upgrades and the framework's SSE `GET`/`POST`/`DELETE` transport.

## Handler model

| Layer | Registration | Context responsibility |
|---|---|---|
| Connection | `CreateEndpoint` | inspect route/query/headers, set initial assigns, accept or decline |
| Join | `Endpoint.CreateChannel` | authorize membership, set channel assigns, track presence, accept or decline |
| Incoming event | `Lobby.OnMessage` | reply, broadcast, update presence/assigns, or evict users |
| Outgoing event | `Lobby.OnOutgoing` | transform or block delivery for a recipient |
| Leave | `Lobby.OnLeave` | observe final state and perform cleanup |

Route values are exposed through `ctx.Route.Param`, `QueryParam`, `ParseParams`, and `ParseQuery`. Payload, assigns, and presence can be decoded into application structs with the `Parse*` methods on each context.

Handlers use `error` returns so middleware and transport failures propagate. A matched connection or join handler must explicitly accept or decline.

## Reliability and security options

`DefaultOptions` enables same-origin browser checks and finite message, queue, heartbeat, and write limits. Important controls include:

- `AllowedOrigins`, `AllowedOriginRegexps`, and `CheckOriginFunc`;
- `MaxMessageSize`, `MaxConnections`, and compression policy;
- `SendChannelBuffer`, `ReceiveChannelBuffer`, and `InternalQueueTimeout`;
- `PingInterval`, `PongWait`, `WriteWait`, and `SendTimeout`;
- `Hooks` for rate limiting, metrics, and connection lifecycle callbacks.

Messages from one transport are processed serially in arrival order. Outbound delivery is ordered per recipient. Full queues and stalled consumers fail within configured bounds rather than growing memory indefinitely.

`AllowAnyOrigin` should be enabled only for deliberately public endpoints. Compression is disabled by default; enabling it for attacker-controlled secrets can introduce compression side channels.

## Redis distribution

```go
redisClient := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
pubsub, err := distributed.NewRedisPubSub(context.Background(), redisClient)
if err != nil {
	log.Fatal(err)
}
defer pubsub.Close()

options := pondsocket.DefaultOptions()
options.PubSub = pubsub
options.Namespace = "my-app"

manager := pondsocket.NewManager(context.Background(), *options)
```

Imports used above:

```go
import (
	"context"
	"log"

	pondsocket "github.com/eleven-am/pondsocket/go/pondsocket"
	"github.com/eleven-am/pondsocket/go/pondsocket/distributed"
	"github.com/redis/go-redis/v9"
)
```

The Redis adapter is documented in [`distributed/README.md`](./distributed/README.md). The cross-runtime topic and payload contract is defined by [`DISTRIBUTED_PROTOCOL.md`](../../DISTRIBUTED_PROTOCOL.md).

Treat Redis and the namespace as a trust boundary: isolate untrusted publishers, restrict credentials and networks, and use transport encryption where required.

## Tests

```bash
go test ./...
go test -race ./...
```

## License

GPL-3.0
