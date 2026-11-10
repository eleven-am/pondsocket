package pondsocket

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestReadMessagesParsesNewlineDelimitedFrameAndIsolatesMalformedLines(t *testing.T) {
	events := []ChannelEvent{
		{Action: Connect, Event: string(EventConnection), RequestID: "first"},
		{Action: System, Event: "second", ChannelName: "room", RequestID: "second"},
	}
	first, err := json.Marshal(events[0])
	if err != nil {
		t.Fatal(err)
	}
	second, err := json.Marshal(events[1])
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		frame := append(append(append(first, '\n'), []byte("{malformed-json}\n\n")...), second...)
		if err := conn.WriteMessage(1, frame); err != nil {
			t.Errorf("write frame: %v", err)
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.PingInterval = 0
	config.ReadTimeout = time.Second
	client, err := NewPondClientWithConfig("ws"+strings.TrimPrefix(server.URL, "http"), nil, config)
	if err != nil {
		t.Fatal(err)
	}
	received, unsubscribe := client.subscribeToEventsWithUnsubscribe()
	defer unsubscribe()
	defer client.Disconnect()
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}

	got := make([]ChannelEvent, 0, 2)
	deadline := time.After(time.Second)
	for len(got) < 2 {
		select {
		case event := <-received:
			got = append(got, event)
		case <-deadline:
			t.Fatalf("received %d valid events, want 2: %#v", len(got), got)
		}
	}
	if got[0].RequestID != "first" || got[1].RequestID != "second" {
		t.Fatalf("events decoded out of order: %#v", got)
	}
}

func TestReadMessagesPreservesLargeNewlineFrameOrder(t *testing.T) {
	const total = 300
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		defer conn.Close()
		var frame strings.Builder
		for i := 0; i < total; i++ {
			data, err := json.Marshal(ChannelEvent{
				Action:      System,
				Event:       "ordered",
				ChannelName: "room",
				RequestID:   fmt.Sprintf("%03d", i),
			})
			if err != nil {
				t.Errorf("marshal: %v", err)
				return
			}
			if i > 0 {
				frame.WriteByte('\n')
			}
			frame.Write(data)
		}
		if err := conn.WriteMessage(1, []byte(frame.String())); err != nil {
			t.Errorf("write: %v", err)
		}
		_, _, _ = conn.ReadMessage()
	}))
	defer server.Close()

	config := DefaultClientConfig()
	config.PingInterval = 0
	config.ReadTimeout = 2 * time.Second
	client, err := NewPondClientWithConfig("ws"+strings.TrimPrefix(server.URL, "http"), nil, config)
	if err != nil {
		t.Fatal(err)
	}
	received, unsubscribe := client.subscribeToEventsWithUnsubscribe()
	defer unsubscribe()
	defer client.Disconnect()
	if err := client.Connect(); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < total; i++ {
		select {
		case event := <-received:
			want := fmt.Sprintf("%03d", i)
			if event.RequestID != want {
				t.Fatalf("event %d request ID = %q, want %q", i, event.RequestID, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out at event %d", i)
		}
	}
}

func TestDisconnectClosesChannelsAndReleasesSubscriptions(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	channel := client.CreateChannel("room", JoinParams{})
	channel.OnMessage(func(string, PondMessage) {})
	channel.OnChannelStateChange(func(ChannelState) {})
	client.OnConnectionChange(func(bool) {})

	if err := client.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if channel.State() != Closed {
		t.Fatalf("channel state = %s, want %s", channel.State(), Closed)
	}
	client.channelsMu.RLock()
	channelCount := len(client.channels)
	client.channelsMu.RUnlock()
	client.connectionSubsMu.RLock()
	connectionSubCount := len(client.connectionSubs)
	client.connectionSubsMu.RUnlock()
	client.eventSubsMu.RLock()
	eventSubCount := len(client.eventSubs)
	client.eventSubsMu.RUnlock()
	if channelCount != 0 || connectionSubCount != 0 || eventSubCount != 0 {
		t.Fatalf("resources retained after disconnect: channels=%d connectionSubs=%d eventSubs=%d", channelCount, connectionSubCount, eventSubCount)
	}
	channel.eventSubsMu.RLock()
	channelEventSubs := len(channel.eventSubs)
	channel.eventSubsMu.RUnlock()
	channel.stateSubsMu.RLock()
	channelStateSubs := len(channel.stateSubs)
	channel.stateSubsMu.RUnlock()
	if channelEventSubs != 0 || channelStateSubs != 0 {
		t.Fatalf("channel subscribers retained: events=%d states=%d", channelEventSubs, channelStateSubs)
	}
}

func TestJoinWaitsForConnectionAndPublishesOnce(t *testing.T) {
	published := make(chan ClientMessage, 4)
	connection := make(chan bool, 4)
	channel := newChannel(func(message ClientMessage) {
		published <- message
	}, connection, nil, "room", JoinParams{}, time.Second, false)
	defer channel.close()

	channel.Join()
	select {
	case message := <-published:
		t.Fatalf("published while disconnected: %#v", message)
	case <-time.After(20 * time.Millisecond):
	}

	connection <- true
	var join ClientMessage
	select {
	case join = <-published:
	case <-time.After(time.Second):
		t.Fatal("join was not published after connection")
	}
	if join.Action != JoinChannel || join.RequestID == "" {
		t.Fatalf("invalid join request: %#v", join)
	}
	connection <- true
	select {
	case duplicate := <-published:
		t.Fatalf("duplicate join published: %#v", duplicate)
	case <-time.After(30 * time.Millisecond):
	}
}

func TestAcknowledgeFlushesQueuedMessagesInFIFOOrder(t *testing.T) {
	var mu sync.Mutex
	published := make([]ClientMessage, 0, 51)
	channel := newChannel(func(message ClientMessage) {
		mu.Lock()
		published = append(published, message)
		mu.Unlock()
	}, make(chan bool), nil, "room", JoinParams{}, time.Second, true)
	defer channel.close()

	channel.Join()
	for i := 0; i < 50; i++ {
		channel.SendMessage("ordered", PondMessage{"index": i})
	}
	mu.Lock()
	joinID := published[0].RequestID
	mu.Unlock()
	channel.acknowledge(joinID, make(chan ChannelEvent))

	mu.Lock()
	defer mu.Unlock()
	if len(published) != 51 {
		t.Fatalf("published %d messages, want join + 50", len(published))
	}
	for i, message := range published[1:] {
		if got := message.Payload.(PondMessage)["index"]; got != i {
			t.Fatalf("message %d payload index = %v", i, got)
		}
	}
}

func TestStaleJoinAcknowledgementIsIgnored(t *testing.T) {
	var mu sync.Mutex
	var published []ClientMessage
	channel := newChannel(func(message ClientMessage) {
		mu.Lock()
		published = append(published, message)
		mu.Unlock()
	}, make(chan bool), nil, "room", JoinParams{}, time.Second, true)
	defer channel.close()

	channel.Join()
	mu.Lock()
	firstID := published[len(published)-1].RequestID
	mu.Unlock()
	channel.clearPendingJoin("")
	channel.sendJoinRequest()
	mu.Lock()
	secondID := published[len(published)-1].RequestID
	mu.Unlock()
	if firstID == secondID {
		t.Fatal("join retry reused request ID")
	}

	var staleCleanup atomic.Int32
	channel.acknowledge(firstID, make(chan ChannelEvent), func() { staleCleanup.Add(1) })
	if channel.State() != Joining {
		t.Fatalf("stale ack changed state to %s", channel.State())
	}
	if staleCleanup.Load() != 1 {
		t.Fatalf("stale ack subscription cleanup count = %d, want 1", staleCleanup.Load())
	}

	channel.acknowledge(secondID, make(chan ChannelEvent))
	if channel.State() != Joined {
		t.Fatalf("current ack did not join channel: %s", channel.State())
	}
}

func TestJoinTimeoutDeclinesAndCleansPendingRequest(t *testing.T) {
	channel := newChannel(func(ClientMessage) {}, make(chan bool), nil, "slow", JoinParams{}, 20*time.Millisecond, true)
	defer channel.close()
	channel.Join()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && channel.State() != Declined {
		time.Sleep(time.Millisecond)
	}
	if channel.State() != Declined {
		t.Fatalf("join timeout left channel in %s", channel.State())
	}
	channel.joinMu.Lock()
	pending := channel.pendingJoinID
	timer := channel.joinTimer
	channel.joinMu.Unlock()
	if pending != "" || timer != nil {
		t.Fatalf("join timeout retained pending state: id=%q timer=%v", pending, timer != nil)
	}
}

func TestRepeatedAcknowledgeReplacesReceiverSubscription(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()
	client.setConnectionState(true)
	channel := client.CreateChannel("room", JoinParams{})

	for i := 0; i < 5; i++ {
		channel.setState(Joining)
		channel.joinMu.Lock()
		channel.pendingJoinID = fmt.Sprintf("join-%d", i)
		channel.joinMu.Unlock()
		client.handleAcknowledge(ChannelEvent{
			Action:      System,
			Event:       string(EventAcknowledge),
			ChannelName: "room",
			RequestID:   fmt.Sprintf("join-%d", i),
		})
		client.eventSubsMu.RLock()
		count := len(client.eventSubs)
		client.eventSubsMu.RUnlock()
		if count != 2 {
			t.Fatalf("iteration %d retained %d event subscriptions, want global + channel = 2", i, count)
		}
	}
}

func TestSendForResponseRejectsInvalidLifetime(t *testing.T) {
	channel := NewChannel(func(ClientMessage) {}, make(chan bool), "room", JoinParams{})
	if response, err := channel.SendForResponse("event", PondMessage{}, 0); err == nil || response != nil {
		t.Fatalf("zero timeout returned response=%v err=%v", response, err)
	}
	channel.close()
	if response, err := channel.SendForResponse("event", PondMessage{}, time.Second); err == nil || response != nil {
		t.Fatalf("closed channel returned response=%v err=%v", response, err)
	}
}

func TestQueueSaturationReturnsErrorWithoutDroppingOldest(t *testing.T) {
	channel := newChannel(func(ClientMessage) {}, make(chan bool), nil, "room", JoinParams{}, time.Second, false)
	defer channel.close()

	for i := 0; i < maxQueueSize; i++ {
		if err := channel.SendMessageChecked("ordered", PondMessage{"index": i}); err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}
	if err := channel.SendMessageChecked("overflow", PondMessage{"index": maxQueueSize}); err == nil {
		t.Fatal("expected an explicit queue-capacity error")
	}

	channel.mu.RLock()
	defer channel.mu.RUnlock()
	if len(channel.queue) != maxQueueSize {
		t.Fatalf("queue length = %d, want %d", len(channel.queue), maxQueueSize)
	}
	if got := channel.queue[0].Payload.(PondMessage)["index"]; got != 0 {
		t.Fatalf("oldest queued message was replaced: index=%v", got)
	}
	if got := channel.queue[len(channel.queue)-1].Payload.(PondMessage)["index"]; got != maxQueueSize-1 {
		t.Fatalf("newest accepted message index=%v, want %d", got, maxQueueSize-1)
	}
}

func TestFailedAckFlushRetainsFIFOForRetry(t *testing.T) {
	var mu sync.Mutex
	var delivered []int
	fail := true
	channel := newChannelWithPublisher(func(message ClientMessage) error {
		if message.Action != Broadcast {
			return nil
		}
		if fail {
			return errors.New("transport write failed")
		}
		mu.Lock()
		delivered = append(delivered, message.Payload.(PondMessage)["index"].(int))
		mu.Unlock()
		return nil
	}, make(chan bool), nil, "room", JoinParams{}, time.Second, false)
	defer channel.close()

	for i := 0; i < 3; i++ {
		if err := channel.SendMessageChecked("ordered", PondMessage{"index": i}); err != nil {
			t.Fatal(err)
		}
	}
	channel.setState(Joining)
	channel.joinMu.Lock()
	channel.pendingJoinID = "join"
	channel.joinMu.Unlock()
	channel.acknowledge("join", make(chan ChannelEvent))

	if channel.State() != Stalled {
		t.Fatalf("state after failed ACK flush = %s, want %s", channel.State(), Stalled)
	}
	channel.mu.RLock()
	queued := len(channel.queue)
	channel.mu.RUnlock()
	if queued != 3 {
		t.Fatalf("failed ACK flush retained %d messages, want 3", queued)
	}

	fail = false
	channel.setState(Joined)
	if err := channel.emptyQueue(); err != nil {
		t.Fatalf("retry flush: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if fmt.Sprint(delivered) != "[0 1 2]" {
		t.Fatalf("retried delivery order = %v, want [0 1 2]", delivered)
	}
}

func TestJoinedSendFailureIsReturnedAndRetained(t *testing.T) {
	wantErr := errors.New("write failed")
	channel := newChannelWithPublisher(func(ClientMessage) error {
		return wantErr
	}, make(chan bool), nil, "room", JoinParams{}, time.Second, true)
	defer channel.close()
	channel.setState(Joined)

	err := channel.SendMessageChecked("event", PondMessage{"value": 1})
	if !errors.Is(err, wantErr) {
		t.Fatalf("SendMessage error = %v, want %v", err, wantErr)
	}
	if channel.State() != Stalled {
		t.Fatalf("state = %s, want %s", channel.State(), Stalled)
	}
	channel.mu.RLock()
	defer channel.mu.RUnlock()
	if len(channel.queue) != 1 || channel.queue[0].Event != "event" {
		t.Fatalf("failed message was not retained: %#v", channel.queue)
	}
}

func TestUnserializableMessageIsRejectedWithoutPoisoningFIFO(t *testing.T) {
	published := 0
	channel := newChannel(func(ClientMessage) { published++ }, make(chan bool), nil, "room", JoinParams{}, time.Second, true)
	defer channel.close()
	channel.setState(Joined)

	err := channel.SendMessageChecked("bad", PondMessage{"value": func() {}})
	if err == nil {
		t.Fatal("expected JSON serialization error")
	}
	if channel.State() != Joined {
		t.Fatalf("state = %s, want %s", channel.State(), Joined)
	}
	if published != 0 {
		t.Fatalf("publisher called %d times for invalid message", published)
	}
	channel.mu.RLock()
	defer channel.mu.RUnlock()
	if len(channel.queue) != 0 {
		t.Fatalf("invalid message poisoned queue: %#v", channel.queue)
	}
}

func TestInitialConnectionNotificationIsSynchronousAndOrdered(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Disconnect()
	client.setConnectionState(true)

	states, unsubscribe := client.subscribeToConnection()
	defer unsubscribe()
	select {
	case state := <-states:
		if !state {
			t.Fatal("initial notification was not the current connected state")
		}
	default:
		t.Fatal("subscribeToConnection returned before publishing its initial state")
	}

	client.setConnectionState(false)
	select {
	case state := <-states:
		if state {
			t.Fatal("disconnect transition was reordered behind stale connected state")
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for disconnect transition")
	}
}
