# PondSocket Go client

Go WebSocket client for PondSocket servers. It supports reconnecting connections, channel state, presence, event subscriptions, request/reply, bounded pre-join queuing, join timeouts, and explicit cleanup.

## Install

```bash
go get github.com/eleven-am/pondsocket/go/pondsocket-client
```

## Quick start

```go
package main

import (
	"fmt"
	"log"
	"time"

	pondsocket "github.com/eleven-am/pondsocket/go/pondsocket-client"
)

func main() {
	client, err := pondsocket.NewPondClient(
		"ws://localhost:8080/v1/socket",
		map[string]any{"token": "connection-token"},
	)
	if err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect()

	unsubscribeConnection := client.OnConnectionChange(func(connected bool) {
		fmt.Println("connected:", connected)
	})
	defer unsubscribeConnection()

	if err := client.Connect(); err != nil {
		log.Fatal(err)
	}

	channel := client.CreateChannel("/chat/general", pondsocket.JoinParams{
		"displayName": "Ada",
	})

	unsubscribeMessage := channel.OnMessageEvent("message", func(payload pondsocket.PondMessage) {
		fmt.Println(payload)
	})
	defer unsubscribeMessage()

	channel.Join()
	if err := channel.SendMessageChecked("message", pondsocket.PondMessage{
		"text": "hello",
	}); err != nil {
		log.Print(err)
	}

	time.Sleep(10 * time.Second)
	if err := channel.LeaveChecked(); err != nil {
		log.Print(err)
	}
}
```

The original `SendMessage` and `Leave` methods remain available for fire-and-forget use. Prefer `SendMessageChecked` and `LeaveChecked` when the caller must observe queue saturation or an immediate transport failure.

## Configuration

Pass a copied `ClientConfig` to `NewPondClientWithConfig`:

```go
config := pondsocket.DefaultClientConfig()
config.ReconnectInterval = 2 * time.Second
config.MaxReconnectTries = 10
config.JoinTimeout = 8 * time.Second
config.WriteTimeout = 5 * time.Second
config.ReadTimeout = 60 * time.Second

client, err := pondsocket.NewPondClientWithConfig(endpoint, params, config)
```

`MaxReconnectTries` defaults to `-1` for unlimited retries. `Disconnect` is terminal for that client instance: it cancels reconnect work, closes channels, removes subscriptions, and releases background goroutines.

## Channel lifecycle

`Channel.State()` returns one of:

- `IDLE`: created but not joining;
- `JOINING`: awaiting server acknowledgement;
- `JOINED`: active;
- `STALLED`: disconnected after joining and waiting to rejoin;
- `DECLINED`: the server rejected or failed the join;
- `CLOSED`: explicitly left or cleaned up.

Subscribe with `OnChannelStateChange`. A join timer prevents a channel from remaining in `JOINING` forever. The pre-join outbound queue is finite; checked sends report saturation instead of allocating without a bound.

## Events, replies, and presence

```go
unsubscribeAll := channel.OnMessage(func(event string, payload pondsocket.PondMessage) {})
unsubscribeOne := channel.OnMessageEvent("message", func(payload pondsocket.PondMessage) {})
unsubscribeJoin := channel.OnJoin(func(user pondsocket.PondPresence) {})
unsubscribeLeave := channel.OnLeave(func(user pondsocket.PondPresence) {})
unsubscribePresence := channel.OnPresenceChange(func(change pondsocket.PresencePayload) {})
unsubscribeUsers := channel.OnUsersChange(func(users []pondsocket.PondPresence) {})

defer unsubscribeAll()
defer unsubscribeOne()
defer unsubscribeJoin()
defer unsubscribeLeave()
defer unsubscribePresence()
defer unsubscribeUsers()
```

For request/reply:

```go
response, err := channel.SendForResponse(
	"ping",
	pondsocket.PondMessage{"sentAt": time.Now().UnixMilli()},
	5*time.Second,
)
if err != nil {
	return err
}

reply := <-response
```

Every subscription returns an idempotent unsubscribe function. Call it when the observer is no longer needed; call `Disconnect` when the client is finished.

## Tests

```bash
go test ./...
go test -race ./...
```

## License

GPL-3.0
