package pondsocket

import (
	"encoding/json"
	"time"
)

// Core types
type PondMessage map[string]interface{}
type PondPresence map[string]interface{}
type JoinParams map[string]interface{}

// Client Actions - actions sent from client to server
type ClientActions string

const (
	JoinChannel  ClientActions = "JOIN_CHANNEL"
	LeaveChannel ClientActions = "LEAVE_CHANNEL"
	Broadcast    ClientActions = "BROADCAST"
)

// Server Actions - actions sent from server to client
type ServerActions string

const (
	Presence ServerActions = "PRESENCE"
	System   ServerActions = "SYSTEM"
	Connect  ServerActions = "CONNECT"
	Error    ServerActions = "ERROR"
)

// Channel State - represents the state of a channel connection
type ChannelState string

const (
	Idle    ChannelState = "IDLE"
	Joining ChannelState = "JOINING"
	Joined  ChannelState = "JOINED"
	Stalled ChannelState = "STALLED"
	Closed  ChannelState = "CLOSED"
)

// Presence Event Types
type PresenceEventTypes string

const (
	PresenceJoin   PresenceEventTypes = "JOIN"
	PresenceLeave  PresenceEventTypes = "LEAVE"
	PresenceUpdate PresenceEventTypes = "UPDATE"
)

// System Events
type Events string

const (
	EventAcknowledge Events = "ACKNOWLEDGE"
	EventConnection  Events = "CONNECTION"
)

// Error Types
type ErrorTypes string

const (
	UnauthorizedConnection  ErrorTypes = "UNAUTHORIZED_CONNECTION"
	UnauthorizedJoinRequest ErrorTypes = "UNAUTHORIZED_JOIN_REQUEST"
	UnauthorizedBroadcast   ErrorTypes = "UNAUTHORIZED_BROADCAST"
	InvalidMessage          ErrorTypes = "INVALID_MESSAGE"
	HandlerNotFound         ErrorTypes = "HANDLER_NOT_FOUND"
	PresenceError           ErrorTypes = "PRESENCE_ERROR"
	ChannelError            ErrorTypes = "CHANNEL_ERROR"
	EndpointError           ErrorTypes = "ENDPOINT_ERROR"
	InternalServerError     ErrorTypes = "INTERNAL_SERVER_ERROR"
)

// ClientMessage represents a message sent from client to server
type ClientMessage struct {
	Action      ClientActions `json:"action"`
	Event       string        `json:"event"`
	Payload     interface{}   `json:"payload"`
	ChannelName string        `json:"channelName"`
	RequestID   string        `json:"requestId"`
}

// ChannelEvent represents a message received from server
type ChannelEvent struct {
	Action      ServerActions `json:"action"`
	Event       string        `json:"event"`
	Payload     interface{}   `json:"payload"`
	ChannelName string        `json:"channelName"`
	RequestID   string        `json:"requestId"`
}

// PresencePayload represents presence change data
type PresencePayload struct {
	Changed  PondPresence   `json:"changed"`
	Presence []PondPresence `json:"presence"`
}

// PondError represents an error from PondSocket
type PondError struct {
	Type    ErrorTypes             `json:"type"`
	Message string                 `json:"message"`
	Details map[string]interface{} `json:"details,omitempty"`
}

func (e *PondError) Error() string {
	return e.Message
}

// Event handler function types
type EventHandler func(event string, payload PondMessage)
type PresenceHandler func(eventType PresenceEventTypes, payload PresencePayload)
type ConnectionHandler func(connected bool)
type ChannelStateHandler func(state ChannelState)

// Client configuration
type ClientConfig struct {
	ReconnectInterval time.Duration
	MaxReconnectTries int
	PingInterval      time.Duration
	PongTimeout       time.Duration
	WriteTimeout      time.Duration
	ReadTimeout       time.Duration
}

// DefaultClientConfig returns sensible default configuration
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ReconnectInterval: 1 * time.Second,
		MaxReconnectTries: -1, // infinite retries
		PingInterval:      30 * time.Second,
		PongTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		ReadTimeout:       60 * time.Second,
	}
}

// Helper function to convert ChannelEvent payload to PresencePayload
func (ce *ChannelEvent) GetPresencePayload() (*PresencePayload, error) {
	var payload PresencePayload
	data, err := json.Marshal(ce.Payload)
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(data, &payload)
	if err != nil {
		return nil, err
	}
	return &payload, nil
}

// Helper function to convert interface{} to PondMessage
func ToPondMessage(data interface{}) PondMessage {
	if msg, ok := data.(map[string]interface{}); ok {
		return PondMessage(msg)
	}
	if msg, ok := data.(PondMessage); ok {
		return msg
	}
	return PondMessage{}
}
