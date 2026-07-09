package pondsocket

import (
	"testing"
	"time"
)

func waitForState(t *testing.T, channel *Channel, want ChannelState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if channel.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("expected channel state %s, got %s", want, channel.State())
}

func TestChannelDeclineOnUnauthorized(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Disconnect()

	channel := client.CreateChannel("lobby", JoinParams{})
	channel.setState(Joining)

	channel.mu.Lock()
	channel.queue = append(channel.queue, ClientMessage{Action: Broadcast, Event: "queued"})
	channel.mu.Unlock()

	client.eventBroadcast <- ChannelEvent{
		Action:      System,
		Event:       string(EventUnauthorized),
		ChannelName: "lobby",
		Payload:     PondMessage{"code": 403, "message": "Forbidden"},
	}

	waitForState(t, channel, Declined, time.Second)

	channel.mu.RLock()
	queueLen := len(channel.queue)
	channel.mu.RUnlock()
	if queueLen != 0 {
		t.Errorf("expected queue to be cleared on decline, got %d messages", queueLen)
	}
}

func TestChannelDeclineOnNotFound(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Disconnect()

	channel := client.CreateChannel("ghost", JoinParams{})
	channel.setState(Joining)

	surfaced := make(chan PondMessage, 1)
	channel.OnMessage(func(event string, payload PondMessage) {
		if event == string(EventNotFound) {
			surfaced <- payload
		}
	})

	client.eventBroadcast <- ChannelEvent{
		Action:      System,
		Event:       string(EventNotFound),
		ChannelName: "ghost",
		Payload:     PondMessage{"code": 404, "message": "Channel not found"},
	}

	waitForState(t, channel, Declined, time.Second)

	select {
	case payload := <-surfaced:
		if payload["code"] != float64(404) && payload["code"] != 404 {
			t.Errorf("expected surfaced code 404, got %v", payload["code"])
		}
	case <-time.After(time.Second):
		t.Fatal("expected NOT_FOUND frame to be surfaced to subscribers")
	}
}

func TestChannelInternalErrorDoesNotChangeState(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Disconnect()

	channel := client.CreateChannel("lobby", JoinParams{})
	channel.setState(Joining)

	surfaced := make(chan PondMessage, 1)
	channel.OnMessage(func(event string, payload PondMessage) {
		if event == string(EventInternalError) {
			surfaced <- payload
		}
	})

	client.eventBroadcast <- ChannelEvent{
		Action:      System,
		Event:       string(EventInternalError),
		ChannelName: "lobby",
		Payload:     PondMessage{"code": 500, "message": "boom"},
	}

	select {
	case <-surfaced:
	case <-time.After(time.Second):
		t.Fatal("expected INTERNAL_ERROR frame to be surfaced to subscribers")
	}

	if channel.State() != Joining {
		t.Errorf("expected state to remain Joining after internal error, got %s", channel.State())
	}
}

func TestPresenceCallbacksFire(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload PondMessage
	}{
		{"fixed server", "JOIN", PondMessage{"changed": PondMessage{"id": "u1"}, "presence": []interface{}{PondMessage{"id": "u1"}}}},
		{"legacy server", "presence:join", PondMessage{"change": PondMessage{"id": "u2"}, "presence": []interface{}{PondMessage{"id": "u2"}}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			publisher := func(msg ClientMessage) {}
			connectionChan := make(chan bool, 1)
			channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
			defer channel.Leave()

			joined := make(chan PondPresence, 1)
			channel.OnJoin(func(p PondPresence) {
				joined <- p
			})

			channel.eventChan <- ChannelEvent{
				Action:      Presence,
				Event:       tt.event,
				ChannelName: "lobby",
				Payload:     tt.payload,
			}

			select {
			case p := <-joined:
				if p["id"] == nil {
					t.Errorf("expected changed presence to carry id, got %v", p)
				}
			case <-time.After(time.Second):
				t.Fatal("expected OnJoin callback to fire")
			}
		})
	}
}

func TestChannelQueueBounded(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	defer channel.Leave()

	total := maxQueueSize + 50
	for i := 0; i < total; i++ {
		channel.SendMessage("evt", PondMessage{"index": i})
	}

	channel.mu.RLock()
	defer channel.mu.RUnlock()

	if len(channel.queue) != maxQueueSize {
		t.Fatalf("expected queue bounded to %d, got %d", maxQueueSize, len(channel.queue))
	}

	firstPayload, ok := channel.queue[0].Payload.(PondMessage)
	if !ok {
		t.Fatalf("unexpected payload type %T", channel.queue[0].Payload)
	}
	if firstPayload["index"] != total-maxQueueSize {
		t.Errorf("expected oldest retained message index %d, got %v", total-maxQueueSize, firstPayload["index"])
	}
}
