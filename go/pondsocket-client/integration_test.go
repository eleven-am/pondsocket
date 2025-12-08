package pondsocket

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Integration tests that test the full client-server interaction

func TestIntegration_BasicFlow(t *testing.T) {
	// Create a mock server that simulates PondSocket behavior
	server := createMockPondSocketServer(t)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create client
	client, err := NewPondClient(wsURL, map[string]interface{}{
		"token": "test-token",
	})
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Track connection state with mutex protection
	var connectionStateMu sync.RWMutex
	var connectionState bool
	unsubConnection := client.OnConnectionChange(func(connected bool) {
		connectionStateMu.Lock()
		connectionState = connected
		connectionStateMu.Unlock()
	})
	defer unsubConnection()

	// Connect
	err = client.Connect()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	connectionStateMu.RLock()
	connState := connectionState
	connectionStateMu.RUnlock()
	if !connState {
		t.Error("Expected connection state to be true")
	}

	// Create channel
	channel := client.CreateChannel("lobby", JoinParams{
		"user_id": "test-user",
		"name":    "Test User",
	})

	// Track channel state with mutex protection
	var channelStateMu sync.RWMutex
	var channelState ChannelState
	unsubChannelState := channel.OnChannelStateChange(func(state ChannelState) {
		channelStateMu.Lock()
		channelState = state
		channelStateMu.Unlock()
	})
	defer unsubChannelState()

	// Track messages with mutex protection
	var receivedMessagesMu sync.Mutex
	var receivedMessages []PondMessage
	unsubMessages := channel.OnMessage(func(event string, payload PondMessage) {
		receivedMessagesMu.Lock()
		receivedMessages = append(receivedMessages, payload)
		receivedMessagesMu.Unlock()
	})
	defer unsubMessages()

	// Track presence with mutex protection
	var currentUsersMu sync.Mutex
	var currentUsers []PondPresence
	unsubPresence := channel.OnUsersChange(func(users []PondPresence) {
		currentUsersMu.Lock()
		currentUsers = users
		currentUsersMu.Unlock()
	})
	defer unsubPresence()

	// Use currentUsers to avoid unused variable warning
	currentUsersMu.Lock()
	_ = currentUsers
	currentUsersMu.Unlock()

	// Join channel
	channel.Join()

	// Wait for join to complete
	time.Sleep(300 * time.Millisecond)

	channelStateMu.RLock()
	chState := channelState
	channelStateMu.RUnlock()
	if chState != Joined {
		t.Errorf("Expected channel state to be Joined, got %s", chState)
	}

	// Send a message
	channel.SendMessage("chat", PondMessage{
		"text":      "Hello World",
		"timestamp": time.Now().Unix(),
	})

	// Wait for message processing
	time.Sleep(200 * time.Millisecond)

	// Should have received the echoed message
	receivedMessagesMu.Lock()
	msgCount := len(receivedMessages)
	receivedMessagesMu.Unlock()
	if msgCount < 1 {
		t.Error("Expected to receive at least one message")
	}

	// Leave channel
	channel.Leave()

	// Wait for leave to complete
	time.Sleep(100 * time.Millisecond)

	channelStateMu.RLock()
	chState = channelState
	channelStateMu.RUnlock()
	if chState != Closed {
		t.Errorf("Expected channel state to be Closed, got %s", chState)
	}

	// Disconnect
	err = client.Disconnect()
	if err != nil {
		t.Fatalf("Failed to disconnect: %v", err)
	}

	// Allow goroutines to finish
	time.Sleep(100 * time.Millisecond)

	// Wait for disconnect
	time.Sleep(100 * time.Millisecond)

	connectionStateMu.RLock()
	finalConnState := connectionState
	connectionStateMu.RUnlock()
	if finalConnState {
		t.Error("Expected connection state to be false after disconnect")
	}
}

func TestIntegration_MultipleChannels(t *testing.T) {
	server := createMockPondSocketServer(t)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewPondClient(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.Connect()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection
	time.Sleep(200 * time.Millisecond)

	// Create multiple channels
	channel1 := client.CreateChannel("lobby", JoinParams{"user_id": "user1"})
	channel2 := client.CreateChannel("general", JoinParams{"user_id": "user1"})
	channel3 := client.CreateChannel("random", JoinParams{"user_id": "user1"})

	// Join all channels
	channel1.Join()
	channel2.Join()
	channel3.Join()

	// Wait for channels to join with timeout
	timeout := time.After(2 * time.Second)
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			t.Fatal("Timeout waiting for channels to join")
		case <-ticker.C:
			if channel1.State() == Joined &&
				channel2.State() == Joined &&
				channel3.State() == Joined {
				goto channelsJoined
			}
		}
	}

channelsJoined:
	// Check states
	if channel1.State() != Joined {
		t.Errorf("Expected channel1 to be Joined, got %s", channel1.State())
	}
	if channel2.State() != Joined {
		t.Errorf("Expected channel2 to be Joined, got %s", channel2.State())
	}
	if channel3.State() != Joined {
		t.Errorf("Expected channel3 to be Joined, got %s", channel3.State())
	}

	// Send messages to different channels
	channel1.SendMessage("chat", PondMessage{"text": "Message to lobby"})
	channel2.SendMessage("chat", PondMessage{"text": "Message to general"})
	channel3.SendMessage("chat", PondMessage{"text": "Message to random"})

	// Wait for message processing
	time.Sleep(200 * time.Millisecond)

	// Leave all channels
	channel1.Leave()
	channel2.Leave()
	channel3.Leave()

	client.Disconnect()
}

func TestIntegration_ReconnectionFlow(t *testing.T) {
	var connectionCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		currentCount := atomic.AddInt32(&connectionCount, 1)

		// Send connection event
		connEvent := ChannelEvent{
			Action: Connect,
			Event:  string(EventConnection),
		}
		conn.WriteJSON(connEvent)

		// If this is the first connection, close immediately to trigger reconnection
		if currentCount == 1 {
			time.Sleep(100 * time.Millisecond)
			return // Connection closes
		}

		// Keep second connection alive longer
		time.Sleep(500 * time.Millisecond)
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create client with fast reconnection
	config := DefaultClientConfig()
	config.ReconnectInterval = 100 * time.Millisecond
	config.MaxReconnectTries = 3

	client, err := NewPondClientWithConfig(wsURL, nil, config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Use mutex-protected slice for connection states
	var connectionStatesMu sync.Mutex
	connectionStates := make([]bool, 0)
	client.OnConnectionChange(func(connected bool) {
		connectionStatesMu.Lock()
		connectionStates = append(connectionStates, connected)
		connectionStatesMu.Unlock()
	})

	// Create channel before connecting
	channel := client.CreateChannel("lobby", JoinParams{"user_id": "test"})

	// Use mutex-protected slice for channel states
	var channelStatesMu sync.Mutex
	channelStates := make([]ChannelState, 0)
	channel.OnChannelStateChange(func(state ChannelState) {
		channelStatesMu.Lock()
		channelStates = append(channelStates, state)
		channelStatesMu.Unlock()
	})

	// Join channel (will be queued since not connected)
	channel.Join()

	// Connect (will trigger reconnection)
	err = client.Connect()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for reconnection to complete
	time.Sleep(800 * time.Millisecond)

	// Should have reconnected
	finalConnCount := atomic.LoadInt32(&connectionCount)
	if finalConnCount < 2 {
		t.Errorf("Expected at least 2 connections, got %d", finalConnCount)
	}

	// Should have multiple connection state changes
	connectionStatesMu.Lock()
	stateCount := len(connectionStates)
	connectionStatesMu.Unlock()
	if stateCount < 2 {
		t.Errorf("Expected at least 2 connection state changes, got %d", stateCount)
	}

	// Channel should eventually be in Joining state (since we don't simulate acknowledgment)
	if channel.State() != Joining {
		t.Errorf("Expected channel state to be Joining, got %s", channel.State())
	}

	client.Disconnect()

	// Allow goroutines to finish
	time.Sleep(100 * time.Millisecond)
}

// Helper function to create a mock PondSocket server
func createMockPondSocketServer(t *testing.T) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade connection: %v", err)
			return
		}
		defer conn.Close()

		// Send connection event
		connEvent := ChannelEvent{
			Action: Connect,
			Event:  string(EventConnection),
		}
		conn.WriteJSON(connEvent)

		// Handle incoming messages
		for {
			var msg ClientMessage
			err := conn.ReadJSON(&msg)
			if err != nil {
				break // Connection closed
			}

			switch msg.Action {
			case JoinChannel:
				// Send acknowledgment
				ackEvent := ChannelEvent{
					Action:      System,
					Event:       string(EventAcknowledge),
					ChannelName: msg.ChannelName,
					RequestID:   msg.RequestID,
				}
				conn.WriteJSON(ackEvent)

				// Send presence update
				presenceEvent := ChannelEvent{
					Action:      Presence,
					Event:       string(PresenceJoin),
					ChannelName: msg.ChannelName,
					Payload: PresencePayload{
						Changed: PondPresence{
							"id":   "test-user",
							"name": "Test User",
						},
						Presence: []PondPresence{
							{
								"id":   "test-user",
								"name": "Test User",
							},
						},
					},
				}
				conn.WriteJSON(presenceEvent)

			case Broadcast:
				// Echo the message back
				echoEvent := ChannelEvent{
					Action:      System,
					Event:       msg.Event,
					Payload:     msg.Payload,
					ChannelName: msg.ChannelName,
					RequestID:   msg.RequestID,
				}
				conn.WriteJSON(echoEvent)

			case LeaveChannel:
				// Send leave confirmation
				leaveEvent := ChannelEvent{
					Action:      Presence,
					Event:       string(PresenceLeave),
					ChannelName: msg.ChannelName,
					Payload: PresencePayload{
						Changed: PondPresence{
							"id":   "test-user",
							"name": "Test User",
						},
						Presence: []PondPresence{},
					},
				}
				conn.WriteJSON(leaveEvent)
			}
		}
	}))
}
