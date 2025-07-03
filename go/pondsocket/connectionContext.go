// This file contains the ConnectionContext struct which provides the interface for handling
// new WebSocket connection requests. It allows accepting or declining connections,
// setting initial metadata, and accessing request information.
package pondsocket

import (
	"context"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"net/http"
)

type ConnectionContext struct {
	accepted         bool
	hasResponded     bool
	request          *http.Request
	response         *http.ResponseWriter
	endpoint         *Endpoint
	userId           string
	assigns          map[string]interface{}
	Route            *Route
	upgrader         websocket.Upgrader
	managerCtx       context.Context
	connectionCxt    context.Context
	connectionCancel context.CancelFunc
}

type connectionOptions struct {
	request  *http.Request
	response *http.ResponseWriter
	endpoint *Endpoint
	userId   string
	upgrader websocket.Upgrader
	connCtx  context.Context
	route    *Route
}

func newConnectionContext(options connectionOptions) *ConnectionContext {
	ctxInternal, cancelInternal := context.WithCancel(options.connCtx)

	return &ConnectionContext{
		request:          options.request,
		endpoint:         options.endpoint,
		userId:           options.userId,
		response:         options.response,
		assigns:          make(map[string]interface{}),
		Route:            options.route,
		upgrader:         options.upgrader,
		managerCtx:       options.endpoint.ctx,
		connectionCxt:    ctxInternal,
		connectionCancel: cancelInternal,
	}
}

// Accept upgrades the HTTP connection to a WebSocket connection and registers it with the endpoint.
// This method must be called to establish the WebSocket connection.
// Returns an error if the connection has already been responded to or if the upgrade fails.
// After accepting, the connection is managed by the endpoint and can join channels.
func (c *ConnectionContext) Accept() error {
	if c.hasResponded {
		c.connectionCancel()
		return badRequest(string(gatewayEntity), "ConnectionContext: the response has already been sent")
	}

	c.hasResponded = true
	c.accepted = true

	wsConn, err := c.upgrader.Upgrade(*c.response, c.request, nil)

	if err != nil {
		c.connectionCancel()
		return wrapF(err, "failed to upgrade connection %s to WebSocket", c.userId)
	}

	connInstance, err := newConn(c.managerCtx, wsConn, c.assigns, c.userId, c.endpoint.options)

	if err != nil {
		c.connectionCancel()

		_ = wsConn.Close()

		return wrapF(err, "failed to create internal connection for %s", c.userId)
	}
	if err = c.endpoint.addConnection(connInstance); err != nil {
		c.connectionCancel()

		return wrapF(err, "failed to add connection %s to endpoint", c.userId)
	}
	return nil
}

// Decline rejects the WebSocket connection request with the specified HTTP status code and message.
// This method sends an HTTP error response and prevents the WebSocket upgrade.
// Returns an error if the connection has already been responded to.
// Common status codes: 401 (Unauthorized), 403 (Forbidden), 429 (Too Many Requests).
func (c *ConnectionContext) Decline(statusCode int, message string) error {
	if c.hasResponded {
		c.connectionCancel()
		return badRequest(string(gatewayEntity), "ConnectionContext: the response has already been sent")
	}

	c.hasResponded = true
	c.accepted = false
	c.connectionCancel()

	http.Error(*c.response, message, statusCode)

	return nil
}

// Reply accepts the connection and immediately sends a system event to the client.
// This is a convenience method that combines Accept() with sending an initial message.
// Useful for sending welcome messages, initial configuration, or authentication challenges.
// Returns an error if the connection cannot be accepted or the message cannot be sent.
func (c *ConnectionContext) Reply(e string, payload interface{}) error {
	err := c.Accept()

	if err != nil {
		return wrapF(err, "failed to accept connection %s for reply", c.userId)
	}
	replyEvent := Event{
		Action:      system,
		ChannelName: string(gatewayEntity),
		RequestId:   uuid.NewString(),
		Event:       e,
		Payload:     payload,
	}
	err = c.endpoint.sendMessage(c.userId, replyEvent)

	if err != nil {
		return wrapF(err, "failed to send reply '%s' to connection %s", e, c.userId)
	}
	return nil
}

// SetAssigns sets a key-value pair in the connection's assigns map.
// Assigns are metadata that persist with the connection across channel joins.
// This data is never sent to the client and is only available server-side.
// Returns the ConnectionContext for method chaining.
func (c *ConnectionContext) SetAssigns(key string, value interface{}) *ConnectionContext {
	if c.assigns == nil {
		c.assigns = make(map[string]interface{})
	}
	c.assigns[key] = value
	return c
}

// GetAssigns retrieves a value from the connection's assigns map by key.
// Returns nil if the key doesn't exist or if assigns haven't been initialized.
// Assigns are server-side metadata that persist with the connection.
func (c *ConnectionContext) GetAssigns(key string) interface{} {
	if c.assigns == nil {
		return nil
	}
	return c.assigns[key]
}

// GetUser returns a User struct representing the connecting client.
// The User contains the auto-generated user ID and any assigns set on this connection.
// Presence will be nil at this stage as it's only available after joining a channel.
func (c *ConnectionContext) GetUser() *User {
	return &User{
		UserID:   c.userId,
		Assigns:  c.assigns,
		Presence: nil,
	}
}

// Cookies returns all HTTP cookies from the connection request.
// This can be used for session management or authentication purposes.
func (c *ConnectionContext) Cookies() []*http.Cookie {
	return c.request.Cookies()
}

// Headers returns the HTTP headers from the connection request.
// This includes all headers sent by the client during the WebSocket upgrade request.
func (c *ConnectionContext) Headers() http.Header {
	return c.request.Header
}

// ParseAssigns unmarshals the connection's assigns into the provided struct.
// This is useful for deserializing connection assigns into typed structs.
// Returns an error if the assigns cannot be parsed into the target type.
func (c *ConnectionContext) ParseAssigns(v interface{}) error {
	return parseAssigns(v, c.assigns)
}

// Context returns the context for this connection.
// The context is cancelled when the connection is closed or declined.
func (c *ConnectionContext) Context() context.Context {
	return c.connectionCxt
}
