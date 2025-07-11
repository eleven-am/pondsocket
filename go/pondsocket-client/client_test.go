package pondsocket

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

// Mock WebSocket server for testing
func createMockServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Fatalf("Failed to upgrade connection: %v", err)
		}
		defer conn.Close()
		handler(conn)
	}))
}

func TestNewPondClient(t *testing.T) {
	// Test with basic endpoint
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	if client.address.String() != "ws://localhost:4000/socket" {
		t.Errorf("Expected address 'ws://localhost:4000/socket', got %s", client.address.String())
	}

	// Test with parameters
	params := map[string]interface{}{
		"token": "test-token",
		"room":  "lobby",
	}

	client, err = NewPondClient("ws://localhost:4000/socket", params)
	if err != nil {
		t.Fatalf("Failed to create client with params: %v", err)
	}

	expectedQuery := "room=lobby&token=test-token"
	if client.address.RawQuery != expectedQuery {
		t.Errorf("Expected query '%s', got '%s'", expectedQuery, client.address.RawQuery)
	}
}

func TestNewPondClient_SchemeConversion(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"http://localhost:4000/socket", "ws://localhost:4000/socket"},
		{"https://localhost:4000/socket", "wss://localhost:4000/socket"},
		{"ws://localhost:4000/socket", "ws://localhost:4000/socket"},
		{"wss://localhost:4000/socket", "wss://localhost:4000/socket"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			client, err := NewPondClient(tt.input, nil)
			if err != nil {
				t.Fatalf("Failed to create client: %v", err)
			}

			if client.address.String() != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, client.address.String())
			}
		})
	}
}

func TestNewPondClient_InvalidScheme(t *testing.T) {
	_, err := NewPondClient("ftp://localhost:4000/socket", nil)
	if err == nil {
		t.Error("Expected error for invalid scheme, got nil")
	}

	if !strings.Contains(err.Error(), "unsupported scheme") {
		t.Errorf("Expected 'unsupported scheme' error, got: %v", err)
	}
}

func TestNewPondClient_InvalidURL(t *testing.T) {
	_, err := NewPondClient("invalid-url", nil)
	if err == nil {
		t.Error("Expected error for invalid URL, got nil")
	}
}

func TestPondClient_GetState(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Initially should be false
	if client.GetState() != false {
		t.Error("Expected initial state to be false")
	}
}

func TestPondClient_CreateChannel(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	params := JoinParams{
		"user_id": "123",
		"name":    "Test User",
	}

	channel := client.CreateChannel("lobby", params)
	if channel == nil {
		t.Fatal("Expected channel to be created, got nil")
	}

	if channel.name != "lobby" {
		t.Errorf("Expected channel name 'lobby', got %s", channel.name)
	}

	if channel.State() != Idle {
		t.Errorf("Expected channel state to be Idle, got %s", channel.State())
	}

	// Creating the same channel again should return the existing one
	channel2 := client.CreateChannel("lobby", params)
	if channel != channel2 {
		t.Error("Expected same channel instance for same name")
	}
}

func TestPondClient_Connect_Disconnect(t *testing.T) {
	// Create mock server
	server := createMockServer(t, func(conn *websocket.Conn) {
		// Send connection event
		connEvent := ChannelEvent{
			Action: Connect,
			Event:  string(EventConnection),
		}
		conn.WriteJSON(connEvent)

		// Keep connection alive for a bit
		time.Sleep(100 * time.Millisecond)
	})
	defer server.Close()

	// Convert http URL to ws URL
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewPondClient(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test connection
	err = client.Connect()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for connection to be established
	time.Sleep(200 * time.Millisecond)

	// Test disconnection
	err = client.Disconnect()
	if err != nil {
		t.Fatalf("Failed to disconnect: %v", err)
	}

	// State should be false after disconnect
	if client.GetState() != false {
		t.Error("Expected state to be false after disconnect")
	}
}

func TestPondClient_OnConnectionChange(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	connected := false
	unsubscribe := client.OnConnectionChange(func(state bool) {
		connected = state
	})
	defer unsubscribe()

	// Initially should receive false
	time.Sleep(50 * time.Millisecond)
	if connected != false {
		t.Error("Expected initial connection state to be false")
	}

	// Simulate connection change
	client.setConnectionState(true)
	time.Sleep(50 * time.Millisecond)

	if connected != true {
		t.Error("Expected connection state to be true after change")
	}
}

func TestPondClient_Reconnection(t *testing.T) {
	reconnectCount := 0

	server := createMockServer(t, func(conn *websocket.Conn) {
		reconnectCount++

		// Send connection event
		connEvent := ChannelEvent{
			Action: Connect,
			Event:  string(EventConnection),
		}
		conn.WriteJSON(connEvent)

		// Close connection immediately to trigger reconnection
		conn.Close()
	})
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	// Create client with fast reconnection
	config := DefaultClientConfig()
	config.ReconnectInterval = 100 * time.Millisecond
	config.MaxReconnectTries = 2

	client, err := NewPondClientWithConfig(wsURL, nil, config)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	err = client.Connect()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for reconnection attempts
	time.Sleep(500 * time.Millisecond)

	// Should have attempted reconnection
	if reconnectCount < 2 {
		t.Errorf("Expected at least 2 connection attempts, got %d", reconnectCount)
	}

	client.Disconnect()
}

func TestPondClient_MessageHandling(t *testing.T) {
	receivedEvents := make([]ChannelEvent, 0)

	server := createMockServer(t, func(conn *websocket.Conn) {
		// Send connection event
		connEvent := ChannelEvent{
			Action: Connect,
			Event:  string(EventConnection),
		}
		conn.WriteJSON(connEvent)

		// Send test message
		testEvent := ChannelEvent{
			Action:      System,
			Event:       "test_event",
			Payload:     PondMessage{"message": "Hello World"},
			ChannelName: "test_channel",
			RequestID:   "req123",
		}
		conn.WriteJSON(testEvent)

		// Keep connection alive
		time.Sleep(200 * time.Millisecond)
	})
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")

	client, err := NewPondClient(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Subscribe to events
	eventChan := client.subscribeToEvents()
	go func() {
		for event := range eventChan {
			receivedEvents = append(receivedEvents, event)
		}
	}()

	err = client.Connect()
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}

	// Wait for messages
	time.Sleep(300 * time.Millisecond)

	client.Disconnect()

	// Should have received at least the connection event
	if len(receivedEvents) < 1 {
		t.Errorf("Expected at least 1 event, got %d", len(receivedEvents))
	}
}

func TestPondClient_CustomConfig(t *testing.T) {
	config := &ClientConfig{
		ReconnectInterval: 2 * time.Second,
		MaxReconnectTries: 5,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      15 * time.Second,
	}

	client, err := NewPondClientWithConfig("ws://localhost:4000/socket", nil, config)
	if err != nil {
		t.Fatalf("Failed to create client with custom config: %v", err)
	}

	if client.config.ReconnectInterval != 2*time.Second {
		t.Errorf("Expected ReconnectInterval 2s, got %v", client.config.ReconnectInterval)
	}

	if client.config.MaxReconnectTries != 5 {
		t.Errorf("Expected MaxReconnectTries 5, got %d", client.config.MaxReconnectTries)
	}
}

func TestPondClient_ConcurrentAccess(t *testing.T) {
	client, err := NewPondClient("ws://localhost:4000/socket", nil)
	if err != nil {
		t.Fatalf("Failed to create client: %v", err)
	}

	// Test concurrent channel creation
	done := make(chan bool)
	channelCount := 10

	for i := 0; i < channelCount; i++ {
		go func(index int) {
			channelName := fmt.Sprintf("channel-%d", index)
			channel := client.CreateChannel(channelName, JoinParams{})
			if channel == nil {
				t.Errorf("Failed to create channel %s", channelName)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < channelCount; i++ {
		<-done
	}

	// Check that all channels were created
	client.channelsMu.RLock()
	if len(client.channels) != channelCount {
		t.Errorf("Expected %d channels, got %d", channelCount, len(client.channels))
	}
	client.channelsMu.RUnlock()
}
