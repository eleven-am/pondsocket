package pondsocket

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAdversarialNotFoundDemotesJoinedChannel(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Disconnect()

	client.setConnectionState(true)

	channel := client.CreateChannel("live", JoinParams{})
	channel.setState(Joined)

	channel.mu.Lock()
	channel.queue = append(channel.queue, ClientMessage{Action: Broadcast, Event: "queued"})
	channel.mu.Unlock()

	surfaced := make(chan PondMessage, 1)
	channel.OnMessage(func(event string, payload PondMessage) {
		if event == string(EventNotFound) {
			surfaced <- payload
		}
	})

	client.eventBroadcast <- ChannelEvent{
		Action:      System,
		Event:       string(EventNotFound),
		ChannelName: "live",
		Payload:     PondMessage{"code": 404, "message": "Channel not found"},
	}

	select {
	case payload := <-surfaced:
		if payload["code"] != float64(404) && payload["code"] != 404 {
			t.Errorf("expected surfaced code 404, got %v", payload["code"])
		}
	case <-time.After(time.Second):
		t.Fatal("expected NOT_FOUND frame to be surfaced to subscribers")
	}

	if channel.State() != Joined {
		t.Errorf("expected JOINED channel to stay JOINED after stray NOT_FOUND, got %s", channel.State())
	}

	channel.mu.RLock()
	queueLen := len(channel.queue)
	channel.mu.RUnlock()
	if queueLen != 1 {
		t.Errorf("expected queue intact (1 message) after stray NOT_FOUND, got %d", queueLen)
	}
}

func TestAdversarialUnauthorizedUnknownChannel(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Disconnect()

	client.eventBroadcast <- ChannelEvent{
		Action:      System,
		Event:       string(EventUnauthorized),
		ChannelName: "does-not-exist",
		Payload:     PondMessage{"code": 401},
	}

	time.Sleep(100 * time.Millisecond)
}

func TestAdversarialDeclinedCanRejoin(t *testing.T) {
	published := make(chan ClientMessage, 4)
	publisher := func(msg ClientMessage) { published <- msg }
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	defer channel.Leave()

	channel.setState(Joining)
	channel.decline(ChannelEvent{
		Action:      System,
		Event:       string(EventUnauthorized),
		ChannelName: "lobby",
		Payload:     PondMessage{"code": 403},
	})
	if channel.State() != Declined {
		t.Fatalf("expected Declined, got %s", channel.State())
	}

	channel.Join()
	if channel.State() != Joining {
		t.Errorf("expected a Declined channel to be able to re-Join (Joining), got %s", channel.State())
	}
	select {
	case msg := <-published:
		if msg.Action != JoinChannel {
			t.Errorf("expected JOIN_CHANNEL publish on rejoin, got %s", msg.Action)
		}
	case <-time.After(time.Second):
		t.Error("expected rejoin to publish a JOIN_CHANNEL message")
	}
}

func TestAdversarialDuplicateDecline(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	defer channel.Leave()

	channel.setState(Joining)
	ev := ChannelEvent{Action: System, Event: string(EventNotFound), ChannelName: "lobby", Payload: PondMessage{"code": 404}}
	channel.decline(ev)
	channel.decline(ev)
	channel.decline(ev)

	if channel.State() != Declined {
		t.Errorf("expected Declined after duplicate declines, got %s", channel.State())
	}
}

func TestAdversarialQueueDropOldestExactBoundary(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	defer channel.Leave()

	for i := 0; i < maxQueueSize; i++ {
		channel.SendMessage("evt", PondMessage{"index": i})
	}
	channel.mu.RLock()
	atCap := len(channel.queue)
	channel.mu.RUnlock()
	if atCap != maxQueueSize {
		t.Fatalf("expected exactly %d queued at cap, got %d", maxQueueSize, atCap)
	}

	channel.SendMessage("evt", PondMessage{"index": maxQueueSize})

	channel.mu.RLock()
	defer channel.mu.RUnlock()
	if len(channel.queue) != maxQueueSize {
		t.Fatalf("expected queue still %d after overflow, got %d", maxQueueSize, len(channel.queue))
	}
	first := channel.queue[0].Payload.(PondMessage)
	if first["index"] != 1 {
		t.Errorf("expected oldest dropped so index 0 is gone and head is 1, got %v", first["index"])
	}
	last := channel.queue[maxQueueSize-1].Payload.(PondMessage)
	if last["index"] != maxQueueSize {
		t.Errorf("expected newest retained index %d, got %v", maxQueueSize, last["index"])
	}
}

func TestAdversarialQueueConcurrentFlush(t *testing.T) {
	var published int64
	publisher := func(msg ClientMessage) { atomic.AddInt64(&published, 1) }
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	defer channel.Leave()

	channel.setState(Joining)

	var wg sync.WaitGroup
	for i := 0; i < 200; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			channel.SendMessage("evt", PondMessage{"index": n})
		}(i)
	}

	time.Sleep(2 * time.Millisecond)
	eventChan := make(chan ChannelEvent)
	channel.Acknowledge(eventChan)

	wg.Wait()

	channel.mu.RLock()
	remaining := len(channel.queue)
	channel.mu.RUnlock()
	if remaining != 0 {
		t.Errorf("expected queue drained to 0 after ACK, got %d", remaining)
	}
	if channel.State() != Joined {
		t.Errorf("expected Joined after Acknowledge, got %s", channel.State())
	}
}
