package pondsocket

import (
	"sync"
	"testing"
	"time"
)

func TestNewChannel(t *testing.T) {
	publisher := func(msg ClientMessage) {
		// Mock publisher
	}

	connectionChan := make(chan bool, 1)
	connectionChan <- true

	params := JoinParams{
		"user_id": "123",
		"name":    "Test User",
	}

	channel := NewChannel(publisher, connectionChan, "test-channel", params)

	if channel == nil {
		t.Fatal("Expected channel to be created, got nil")
	}

	if channel.name != "test-channel" {
		t.Errorf("Expected channel name 'test-channel', got %s", channel.name)
	}

	if channel.State() != Idle {
		t.Errorf("Expected initial state to be Idle, got %s", channel.State())
	}

	if len(channel.presence) != 0 {
		t.Errorf("Expected empty presence list, got %d items", len(channel.presence))
	}
}

func TestChannel_State(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)

	channel := NewChannel(publisher, connectionChan, "test", JoinParams{})

	// Test initial state
	if channel.State() != Idle {
		t.Errorf("Expected initial state Idle, got %s", channel.State())
	}

	// Test state changes
	states := []ChannelState{Joining, Joined, Stalled, Closed}

	for _, state := range states {
		channel.setState(state)
		if channel.State() != state {
			t.Errorf("Expected state %s, got %s", state, channel.State())
		}
	}
}

func TestChannel_Join(t *testing.T) {
	var publishedMessage ClientMessage
	var publishCalled bool

	publisher := func(msg ClientMessage) {
		publishedMessage = msg
		publishCalled = true
	}

	connectionChan := make(chan bool, 1)
	connectionChan <- true // Simulate connected state

	params := JoinParams{
		"user_id": "123",
		"role":    "member",
	}

	channel := NewChannel(publisher, connectionChan, "lobby", params)

	// Test join when not connected
	channel.Join()

	// Should change state to Joining
	if channel.State() != Joining {
		t.Errorf("Expected state Joining after join, got %s", channel.State())
	}

	// Should have published join message
	time.Sleep(50 * time.Millisecond) // Wait for publisher to be called
	if !publishCalled {
		t.Error("Expected publisher to be called")
	}

	if publishedMessage.Action != JoinChannel {
		t.Errorf("Expected action JoinChannel, got %s", publishedMessage.Action)
	}

	if publishedMessage.ChannelName != "lobby" {
		t.Errorf("Expected channel name 'lobby', got %s", publishedMessage.ChannelName)
	}

	// Test join when already joined
	channel.setState(Joined)
	publishCalled = false
	channel.Join()

	time.Sleep(50 * time.Millisecond)
	// Should not call publisher again
	if publishCalled {
		t.Error("Expected publisher not to be called when already joined")
	}
}

func TestChannel_Leave(t *testing.T) {
	var publishedMessage ClientMessage
	var publishCalled bool

	publisher := func(msg ClientMessage) {
		publishedMessage = msg
		publishCalled = true
	}

	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	channel.Leave()

	// Should change state to Closed
	if channel.State() != Closed {
		t.Errorf("Expected state Closed after leave, got %s", channel.State())
	}

	// Should have published leave message
	if !publishCalled {
		t.Error("Expected publisher to be called")
	}

	if publishedMessage.Action != LeaveChannel {
		t.Errorf("Expected action LeaveChannel, got %s", publishedMessage.Action)
	}
}

func TestChannel_SendMessage(t *testing.T) {
	var publishedMessage ClientMessage
	var publishCalled bool

	publisher := func(msg ClientMessage) {
		publishedMessage = msg
		publishCalled = true
	}

	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	// Set to joined state so message is sent immediately
	channel.setState(Joined)

	payload := PondMessage{
		"text":      "Hello World",
		"timestamp": 1234567890,
	}

	channel.SendMessage("chat", payload)

	if !publishCalled {
		t.Error("Expected publisher to be called")
	}

	if publishedMessage.Action != Broadcast {
		t.Errorf("Expected action Broadcast, got %s", publishedMessage.Action)
	}

	if publishedMessage.Event != "chat" {
		t.Errorf("Expected event 'chat', got %s", publishedMessage.Event)
	}

	if publishedMessage.ChannelName != "lobby" {
		t.Errorf("Expected channel name 'lobby', got %s", publishedMessage.ChannelName)
	}
}

func TestChannel_SendForResponse(t *testing.T) {
	var publishedMessage ClientMessage

	publisher := func(msg ClientMessage) {
		publishedMessage = msg
	}

	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	// Set to joined state
	channel.setState(Joined)

	payload := PondMessage{"data": "ping"}

	responseChan, err := channel.SendForResponse("ping", payload, 1*time.Second)
	if err != nil {
		t.Fatalf("Failed to send message for response: %v", err)
	}

	// Verify message was sent
	if publishedMessage.Action != Broadcast {
		t.Errorf("Expected action Broadcast, got %s", publishedMessage.Action)
	}

	if publishedMessage.Event != "ping" {
		t.Errorf("Expected event 'ping', got %s", publishedMessage.Event)
	}

	// Test timeout
	select {
	case <-responseChan:
		t.Error("Expected timeout, but received response")
	case <-time.After(1500 * time.Millisecond):
		// Expected timeout
	}
}

func TestChannel_OnMessage(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	// Use channels for thread-safe communication
	resultChan := make(chan struct {
		event   string
		payload PondMessage
	}, 1)

	unsubscribe := channel.OnMessage(func(event string, payload PondMessage) {
		resultChan <- struct {
			event   string
			payload PondMessage
		}{event: event, payload: payload}
	})
	defer unsubscribe()

	// Simulate receiving a message
	testEvent := ChannelEvent{
		Action:      System,
		Event:       "test_message",
		Payload:     PondMessage{"text": "Hello"},
		ChannelName: "lobby",
		RequestID:   "req123",
	}

	// Send event to channel
	select {
	case channel.eventChan <- testEvent:
	default:
		t.Error("Failed to send event to channel")
	}

	// Wait for callback with timeout
	select {
	case result := <-resultChan:
		if result.event != "test_message" {
			t.Errorf("Expected event 'test_message', got %s", result.event)
		}
		if result.payload["text"] != "Hello" {
			t.Errorf("Expected payload text 'Hello', got %v", result.payload["text"])
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for callback")
	}
}

func TestChannel_OnMessageEvent(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	var receivedPayload PondMessage
	var callbackCalled bool

	unsubscribe := channel.OnMessageEvent("chat", func(payload PondMessage) {
		receivedPayload = payload
		callbackCalled = true
	})
	defer unsubscribe()

	// Send matching event
	chatEvent := ChannelEvent{
		Action:      System,
		Event:       "chat",
		Payload:     PondMessage{"text": "Hello"},
		ChannelName: "lobby",
	}

	select {
	case channel.eventChan <- chatEvent:
	default:
		t.Error("Failed to send event to channel")
	}

	time.Sleep(100 * time.Millisecond)

	if !callbackCalled {
		t.Error("Expected callback to be called for matching event")
	}

	if receivedPayload["text"] != "Hello" {
		t.Errorf("Expected payload text 'Hello', got %v", receivedPayload["text"])
	}

	// Send non-matching event
	callbackCalled = false
	otherEvent := ChannelEvent{
		Action:      System,
		Event:       "other",
		Payload:     PondMessage{"text": "World"},
		ChannelName: "lobby",
	}

	select {
	case channel.eventChan <- otherEvent:
	default:
		t.Error("Failed to send event to channel")
	}

	time.Sleep(100 * time.Millisecond)

	if callbackCalled {
		t.Error("Expected callback not to be called for non-matching event")
	}
}

func TestChannel_OnChannelStateChange(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	var receivedState ChannelState
	var callbackCalled bool

	unsubscribe := channel.OnChannelStateChange(func(state ChannelState) {
		receivedState = state
		callbackCalled = true
	})
	defer unsubscribe()

	// Should receive current state immediately
	time.Sleep(100 * time.Millisecond)

	if !callbackCalled {
		t.Error("Expected callback to be called immediately with current state")
	}

	if receivedState != Idle {
		t.Errorf("Expected initial state Idle, got %s", receivedState)
	}

	// Change state
	callbackCalled = false
	channel.setState(Joined)

	time.Sleep(100 * time.Millisecond)

	if !callbackCalled {
		t.Error("Expected callback to be called after state change")
	}

	if receivedState != Joined {
		t.Errorf("Expected state Joined, got %s", receivedState)
	}
}

func TestChannel_Presence(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	// Initial presence should be empty
	presence := channel.Presence()
	if len(presence) != 0 {
		t.Errorf("Expected empty presence, got %d items", len(presence))
	}

	// Set presence data
	testPresence := []PondPresence{
		{"id": "user1", "name": "Alice"},
		{"id": "user2", "name": "Bob"},
	}

	channel.mu.Lock()
	channel.presence = testPresence
	channel.mu.Unlock()

	// Get presence
	presence = channel.Presence()
	if len(presence) != 2 {
		t.Errorf("Expected 2 users in presence, got %d", len(presence))
	}

	// Should be a copy, not the original slice
	if &presence[0] == &channel.presence[0] {
		t.Error("Expected presence to be a copy, not the original slice")
	}
}

func TestChannel_MessageQueuing(t *testing.T) {
	var publishedMessages []ClientMessage
	var mu sync.Mutex

	publisher := func(msg ClientMessage) {
		mu.Lock()
		defer mu.Unlock()
		publishedMessages = append(publishedMessages, msg)
	}

	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	// Channel is in Idle state, messages should be queued
	channel.SendMessage("msg1", PondMessage{"text": "First"})
	channel.SendMessage("msg2", PondMessage{"text": "Second"})

	// No messages should be published yet
	mu.Lock()
	publishedCount := len(publishedMessages)
	mu.Unlock()

	if publishedCount != 0 {
		t.Errorf("Expected 0 published messages, got %d", publishedCount)
	}

	// Check queue length
	channel.mu.RLock()
	queueLength := len(channel.queue)
	channel.mu.RUnlock()

	if queueLength != 2 {
		t.Errorf("Expected 2 queued messages, got %d", queueLength)
	}

	// Join channel (simulate acknowledgment)
	channel.setState(Joined)
	channel.emptyQueue()

	// Now messages should be published
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	publishedCount = len(publishedMessages)
	mu.Unlock()

	if publishedCount != 2 {
		t.Errorf("Expected 2 published messages after join, got %d", publishedCount)
	}
}

func TestChannel_ConcurrentAccess(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	// Set to joined state
	channel.setState(Joined)

	// Test concurrent message sending
	done := make(chan bool)
	messageCount := 100

	for i := 0; i < messageCount; i++ {
		go func(index int) {
			channel.SendMessage("test", PondMessage{"index": index})
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < messageCount; i++ {
		<-done
	}

	// Test concurrent state changes
	for i := 0; i < 10; i++ {
		go func() {
			channel.setState(Joined)
			channel.setState(Stalled)
			done <- true
		}()
	}

	// Wait for all state changes to complete
	for i := 0; i < 10; i++ {
		<-done
	}

	// Should still be in a valid state
	state := channel.State()
	validStates := []ChannelState{Idle, Joining, Joined, Stalled, Closed}
	isValid := false
	for _, validState := range validStates {
		if state == validState {
			isValid = true
			break
		}
	}

	if !isValid {
		t.Errorf("Channel ended up in invalid state: %s", state)
	}
}

func TestChannel_Acknowledge(t *testing.T) {
	publisher := func(msg ClientMessage) {}
	connectionChan := make(chan bool, 1)
	channel := NewChannel(publisher, connectionChan, "lobby", JoinParams{})

	// Add some messages to queue
	channel.SendMessage("msg1", PondMessage{"text": "First"})
	channel.SendMessage("msg2", PondMessage{"text": "Second"})

	// Verify queue has messages
	channel.mu.RLock()
	queueLength := len(channel.queue)
	channel.mu.RUnlock()

	if queueLength != 2 {
		t.Errorf("Expected 2 queued messages, got %d", queueLength)
	}

	// Create event channel
	eventChan := make(chan ChannelEvent, 10)

	// Acknowledge
	channel.Acknowledge(eventChan)

	// Should change state to Joined
	if channel.State() != Joined {
		t.Errorf("Expected state Joined after acknowledge, got %s", channel.State())
	}

	// Queue should be empty
	channel.mu.RLock()
	queueLength = len(channel.queue)
	channel.mu.RUnlock()

	if queueLength != 0 {
		t.Errorf("Expected empty queue after acknowledge, got %d messages", queueLength)
	}
}
