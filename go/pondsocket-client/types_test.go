package pondsocket

import (
	"encoding/json"
	"testing"
)

func TestPondMessage(t *testing.T) {
	msg := PondMessage{
		"text":      "Hello World",
		"timestamp": 1234567890,
		"user":      "john_doe",
	}

	// Test JSON marshaling
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal PondMessage: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaled PondMessage
	err = json.Unmarshal(data, &unmarshaled)
	if err != nil {
		t.Fatalf("Failed to unmarshal PondMessage: %v", err)
	}

	// Verify content
	if unmarshaled["text"] != "Hello World" {
		t.Errorf("Expected text 'Hello World', got %v", unmarshaled["text"])
	}
}

func TestChannelEvent_GetPresencePayload(t *testing.T) {
	// Create a valid presence payload
	presenceData := PresencePayload{
		Changed: PondPresence{
			"id":   "user123",
			"name": "John Doe",
		},
		Presence: []PondPresence{
			{
				"id":   "user123",
				"name": "John Doe",
			},
			{
				"id":   "user456",
				"name": "Jane Smith",
			},
		},
	}

	// Create a channel event with presence payload
	event := ChannelEvent{
		Action:      Presence,
		Event:       string(PresenceJoin),
		Payload:     presenceData,
		ChannelName: "lobby",
		RequestID:   "req123",
	}

	// Test GetPresencePayload method
	payload, err := event.GetPresencePayload()
	if err != nil {
		t.Fatalf("Failed to get presence payload: %v", err)
	}

	// Verify the payload
	if payload.Changed["id"] != "user123" {
		t.Errorf("Expected changed user id 'user123', got %v", payload.Changed["id"])
	}

	if len(payload.Presence) != 2 {
		t.Errorf("Expected 2 users in presence, got %d", len(payload.Presence))
	}
}

func TestToPondMessage(t *testing.T) {
	tests := []struct {
		name     string
		input    interface{}
		expected PondMessage
	}{
		{
			name: "Map string interface",
			input: map[string]interface{}{
				"key": "value",
				"num": 42,
			},
			expected: PondMessage{
				"key": "value",
				"num": 42,
			},
		},
		{
			name: "Existing PondMessage",
			input: PondMessage{
				"test": "data",
			},
			expected: PondMessage{
				"test": "data",
			},
		},
		{
			name:     "Invalid input",
			input:    "invalid",
			expected: PondMessage{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ToPondMessage(tt.input)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d items, got %d", len(tt.expected), len(result))
				return
			}

			for key, expectedValue := range tt.expected {
				if result[key] != expectedValue {
					t.Errorf("Expected %s=%v, got %v", key, expectedValue, result[key])
				}
			}
		})
	}
}

func TestChannelState_String(t *testing.T) {
	tests := []struct {
		state    ChannelState
		expected string
	}{
		{Idle, "IDLE"},
		{Joining, "JOINING"},
		{Joined, "JOINED"},
		{Stalled, "STALLED"},
		{Closed, "CLOSED"},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			if string(tt.state) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, string(tt.state))
			}
		})
	}
}

func TestPresenceEventTypes_String(t *testing.T) {
	tests := []struct {
		eventType PresenceEventTypes
		expected  string
	}{
		{PresenceJoin, "JOIN"},
		{PresenceLeave, "LEAVE"},
		{PresenceUpdate, "UPDATE"},
	}

	for _, tt := range tests {
		t.Run(string(tt.eventType), func(t *testing.T) {
			if string(tt.eventType) != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, string(tt.eventType))
			}
		})
	}
}

func TestPondError_Error(t *testing.T) {
	err := &PondError{
		Type:    UnauthorizedConnection,
		Message: "Connection not authorized",
		Details: map[string]interface{}{
			"code": 401,
		},
	}

	expected := "Connection not authorized"
	if err.Error() != expected {
		t.Errorf("Expected error message '%s', got '%s'", expected, err.Error())
	}
}

func TestDefaultClientConfig(t *testing.T) {
	config := DefaultClientConfig()

	if config.ReconnectInterval.Seconds() != 1.0 {
		t.Errorf("Expected ReconnectInterval of 1 second, got %v", config.ReconnectInterval)
	}

	if config.MaxReconnectTries != -1 {
		t.Errorf("Expected MaxReconnectTries of -1, got %d", config.MaxReconnectTries)
	}

	if config.ReadTimeout.Seconds() != 60.0 {
		t.Errorf("Expected ReadTimeout of 60 seconds, got %v", config.ReadTimeout)
	}

	if config.WriteTimeout.Seconds() != 10.0 {
		t.Errorf("Expected WriteTimeout of 10 seconds, got %v", config.WriteTimeout)
	}
}
