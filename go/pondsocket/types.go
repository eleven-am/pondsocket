// This file contains type definitions for PondSocket including event structures,
// configuration options, interfaces, and constants used throughout the library.
package pondsocket

import (
	"context"
	"crypto/tls"
	"regexp"
	"time"
)

// Event represents a message that flows through the PondSocket system.
// It contains the action type, target channel, unique request ID, payload data,
// event name for routing and processing WebSocket messages, and optional nodeID
// for distributed deployments to prevent processing own messages.
type Event struct {
	Action      action      `json:"action"`
	ChannelName string      `json:"channelName"`
	RequestId   string      `json:"requestId"`
	Payload     interface{} `json:"payload"`
	Event       string      `json:"event"`
	NodeID      string      `json:"nodeId,omitempty"`
}

type internalEvent struct {
	Event
	Recipients *array[string]
}

type messageEvent struct {
	Event *Event
	User  *User
}

type presenceEventType string

type assignsEventType string

// Validate checks if the Event has all required fields populated.
// Returns true if Action, ChannelName, Event, and RequestId are all non-empty,
// false otherwise.
func (e *Event) Validate() bool {
	if e.Action == "" || e.ChannelName == "" || e.Event == "" || e.RequestId == "" {
		return false
	}
	return true
}

type nextFunc func() error

type handlerFunc[Request any, Response any] func(ctx context.Context, request Request, response Response, next nextFunc) error

type action string

type Path string

type eventType string

type entity string

type options struct {
	Name                 string
	Middleware           *middleware[*messageEvent, *Channel]
	Outgoing             *middleware[*OutgoingContext, interface{}]
	Leave                *LeaveHandler
	OnDestroy            func() error
	InternalQueueTimeout time.Duration
	PubSub               PubSub
	Hooks                *Hooks
}

type recipient string

type recipients struct {
	userIds   []string
	recipient *recipient
}

const (
	join                 presenceEventType = "presence:join"
	leave                presenceEventType = "presence:leave"
	update               presenceEventType = "presence:update"
	syncRequest          presenceEventType = "presence:sync_request"
	syncResponse         presenceEventType = "presence:sync_response"
	syncComplete         presenceEventType = "presence:sync_complete"
	assignsSyncRequest   assignsEventType  = "assigns:sync_request"
	assignsSyncResponse  assignsEventType  = "assigns:sync_response"
	assignsSyncComplete  assignsEventType  = "assigns:sync_complete"
	all                  recipient         = "all"
	allExceptSender      recipient         = "all_except_sender"
	presence             action            = "PRESENCE"
	assigns              action            = "ASSIGNS"
	system               action            = "SYSTEM"
	connect              action            = "CONNECT"
	broadcast            action            = "BROADCAST"
	joinChannelEvent     action            = "JOIN_CHANNEL"
	leaveChannelEvent    action            = "LEAVE_CHANNEL"
	acknowledgeEvent     eventType         = "ACKNOWLEDGE"
	exitAcknowledgeEvent eventType         = "EXIT_ACKNOWLEDGE"
	connectionEvent      eventType         = "CONNECTION"
	internalErrorEvent   eventType         = "INTERNAL_ERROR"
	notFoundEvent        eventType         = "NOT_FOUND"
	unauthorizedEvent    eventType         = "UNAUTHORIZED"
	gatewayEntity        entity            = "GATEWAY"
	channelEntity        entity            = "CHANNEL"
)

const (
	StatusBadRequest          = 400
	StatusUnauthorized        = 401
	StatusForbidden           = 403
	StatusNotFound            = 404
	StatusConflict            = 409
	StatusInternalServerError = 500
	StatusServiceUnavailable  = 503
	StatusGatewayTimeout      = 504
	StatusTooManyRequests     = 429
)

// Error represents an error response in the PondSocket system.
// It includes the channel context (if applicable), error message, HTTP-like status code,
// whether the error is temporary (retryable), and optional additional details.
type Error struct {
	ChannelName string      `json:"channelName,omitempty"`
	Message     string      `json:"message"`
	Code        int         `json:"code"`
	Temporary   bool        `json:"temporary"`
	Details     interface{} `json:"details,omitempty"`
	cause       error
}

// LeaveHandler is a callback function invoked when a user leaves a channel.
// It receives a LeaveContext with information about the leaving user and the channel state.
type LeaveHandler func(ctx *LeaveContext)

// User represents a connected user in a channel.
// It contains the unique user identifier, server-side assigns metadata (never sent to clients),
// and presence data that can be shared with other users in the channel.
type User struct {
	UserID   string
	Assigns  map[string]interface{}
	Presence interface{}
}

// Route contains parsed route information from URL patterns.
// It includes query parameters, path parameters, and optional wildcard segments.
type Route struct {
	query    map[string][]string
	params   map[string]string
	Wildcard *string
}

// FinalHandlerFunc is a generic handler function type that processes requests and responses
// after all middleware has been executed.
type FinalHandlerFunc[Request any, Response any] func(request Request, response Response) error

// HandlerFunc is a generic handler function type that processes context objects.
type HandlerFunc[Context any] func(ctx *Context) error

// MessageEventHandler processes incoming message events in a channel context.
type MessageEventHandler HandlerFunc[EventContext]

// JoinEventHandler processes channel join requests.
type JoinEventHandler HandlerFunc[JoinContext]

// ConnectionEventHandler processes new WebSocket connection events.
type ConnectionEventHandler HandlerFunc[ConnectionContext]

// OutgoingEventHandler processes outgoing messages before they are sent to clients.
type OutgoingEventHandler HandlerFunc[OutgoingContext]

// LeaveEventHandler processes user leave events from a channel.
type LeaveEventHandler HandlerFunc[LeaveContext]

// Options configures WebSocket server behavior and connection parameters.
// It includes settings for origin checking, buffer sizes, timeouts, connection limits,
// hooks for extensibility, and PubSub for distributed deployments.
type Options struct {
	CheckOrigin          bool
	AllowedOrigins       []string
	AllowedOriginRegexps []*regexp.Regexp
	ReadBufferSize       int
	WriteBufferSize      int
	MaxMessageSize       int64
	PingInterval         time.Duration
	PongWait             time.Duration
	WriteWait            time.Duration
	EnableCompression    bool
	MaxConnections       int
	SendChannelBuffer    int
	ReceiveChannelBuffer int
	InternalQueueTimeout time.Duration
	Hooks                *Hooks
	PubSub               PubSub
}

// ServerOptions configures the HTTP server that hosts the WebSocket endpoints.
// It includes the WebSocket options, server address, timeout settings, and optional TLS configuration.
type ServerOptions struct {
	Options            *Options
	ServerAddr         string
	ServerReadTimeout  time.Duration
	ServerWriteTimeout time.Duration
	ServerIdleTimeout  time.Duration
	ServerTLSConfig    *tls.Config
}

type contextKey string

const connectionContextKey contextKey = "pondsocket:connection"
