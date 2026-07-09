package pondsocket

import (
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAdversarialDisconnectDropsFinalState(t *testing.T) {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	const iterations = 300
	drops := 0
	for i := 0; i < iterations; i++ {
		client, err := NewPondClient("ws://localhost:4000/socket", nil)
		if err != nil {
			t.Fatalf("create client: %v", err)
		}

		client.setConnectionState(true)

		var mu sync.Mutex
		observed := true
		client.OnConnectionChange(func(connected bool) {
			mu.Lock()
			observed = connected
			mu.Unlock()
		})
		time.Sleep(time.Millisecond)

		_ = client.Disconnect()
		time.Sleep(3 * time.Millisecond)

		mu.Lock()
		final := observed
		mu.Unlock()
		if final {
			drops++
		}
	}
	t.Logf("unit-level disconnect drops: %d/%d (0 here; see stress test for the real-world path)", drops, iterations)
}

func TestAdversarialDisconnectFinalStateStress(t *testing.T) {
	server := createMockPondSocketServer(t)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	const cycles = 200
	drops := 0
	for i := 0; i < cycles; i++ {
		client, err := NewPondClientWithConfig(wsURL, nil, DefaultClientConfig())
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		var mu sync.Mutex
		var last bool
		var seen bool
		client.OnConnectionChange(func(connected bool) {
			mu.Lock()
			last = connected
			seen = true
			mu.Unlock()
		})
		if err := client.Connect(); err != nil {
			t.Fatalf("connect: %v", err)
		}
		deadline := time.Now().Add(500 * time.Millisecond)
		for time.Now().Before(deadline) {
			if client.GetState() {
				break
			}
			time.Sleep(2 * time.Millisecond)
		}
		_ = client.Disconnect()
		time.Sleep(15 * time.Millisecond)
		mu.Lock()
		final := last
		observed := seen
		mu.Unlock()
		if observed && final {
			drops++
		}
	}
	if drops > 0 {
		t.Errorf("OnConnectionChange delivered a stale 'true' as the final state after Disconnect in %d/%d cycles", drops, cycles)
	}
}

func TestAdversarialLeaveNeverJoinedStaysClosed(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "ghost", JoinParams{})

	channel.Leave()
	if channel.State() != Closed {
		t.Fatalf("expected Closed after Leave, got %s", channel.State())
	}

	channel.decline(ChannelEvent{
		Action:      System,
		Event:       string(EventNotFound),
		ChannelName: "ghost",
		Payload:     PondMessage{"code": 404},
	})
	if channel.State() != Closed {
		t.Errorf("a late NOT_FOUND must not revive/redeclare a Closed channel, got %s", channel.State())
	}
}

func TestAdversarialDoubleLeave(t *testing.T) {
	var publishes int
	var mu sync.Mutex
	publisher := func(msg ClientMessage) {
		mu.Lock()
		publishes++
		mu.Unlock()
	}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	channel.setState(Joined)

	channel.Leave()
	channel.Leave()
	channel.Leave()

	if channel.State() != Closed {
		t.Errorf("expected Closed, got %s", channel.State())
	}
	mu.Lock()
	got := publishes
	mu.Unlock()
	if got != 1 {
		t.Errorf("expected exactly one LEAVE_CHANNEL publish across triple Leave, got %d", got)
	}
}

func TestAdversarialLeaveWhileJoining(t *testing.T) {
	published := make(chan ClientMessage, 4)
	publisher := func(msg ClientMessage) { published <- msg }
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	channel.setState(Joining)
	channel.Leave()

	if channel.State() != Closed {
		t.Errorf("expected Closed after Leave-while-Joining, got %s", channel.State())
	}
	select {
	case msg := <-published:
		if msg.Action != LeaveChannel {
			t.Errorf("expected LEAVE_CHANNEL publish, got %s", msg.Action)
		}
	case <-time.After(time.Second):
		t.Error("expected a LEAVE_CHANNEL to be published even while Joining")
	}
}

func TestAdversarialReCreateChannelAfterLeave(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	defer client.Disconnect()

	ch1 := client.CreateChannel("lobby", JoinParams{})
	ch1.setState(Joined)
	ch1.Leave()
	if ch1.State() != Closed {
		t.Fatalf("expected ch1 Closed, got %s", ch1.State())
	}

	ch2 := client.CreateChannel("lobby", JoinParams{})
	if ch2 == ch1 {
		t.Error("CreateChannel after Leave must return a FRESH channel, not the Closed one")
	}
	if ch2.State() == Closed {
		t.Errorf("fresh channel must not be Closed, got %s", ch2.State())
	}
}

func TestAdversarialSendForResponseTimeout(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	defer channel.Leave()
	channel.setState(Joined)

	respChan, err := channel.SendForResponse("ask", PondMessage{"q": 1}, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("SendForResponse: %v", err)
	}

	select {
	case msg, ok := <-respChan:
		if ok {
			t.Errorf("expected channel closed on timeout with no value, got %v", msg)
		}
	case <-time.After(time.Second):
		t.Error("expected SendForResponse to close its response channel on timeout")
	}
}

func TestAdversarialSendForResponseLateReply(t *testing.T) {
	published := make(chan ClientMessage, 1)
	publisher := func(msg ClientMessage) {
		select {
		case published <- msg:
		default:
		}
	}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})
	defer channel.Leave()
	channel.setState(Joined)

	respChan, err := channel.SendForResponse("ask", PondMessage{"q": 1}, 30*time.Millisecond)
	if err != nil {
		t.Fatalf("SendForResponse: %v", err)
	}
	var reqID string
	select {
	case m := <-published:
		reqID = m.RequestID
	case <-time.After(time.Second):
		t.Fatal("no publish captured")
	}

	time.Sleep(60 * time.Millisecond)

	channel.eventChan <- ChannelEvent{
		Action:      System,
		Event:       "ask",
		ChannelName: "lobby",
		RequestID:   reqID,
		Payload:     PondMessage{"a": 2},
	}

	drained := false
	for !drained {
		select {
		case _, ok := <-respChan:
			if !ok {
				drained = true
			}
		case <-time.After(200 * time.Millisecond):
			drained = true
		}
	}
}
