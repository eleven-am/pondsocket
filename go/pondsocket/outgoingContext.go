// This file contains the OutgoingContext struct which provides the interface for handling
// outgoing messages before they are sent to clients. It allows transforming message payloads,
// blocking messages, and refreshing user data.
package pondsocket

import (
	"context"
	"sync"
)

type OutgoingContext struct {
	Channel        *Channel
	Event          *Event
	User           *User
	ctx            context.Context
	err            error
	hasTransformed bool
	isBlocked      bool
	Route          *Route
	mutex          sync.RWMutex
	conn           *Conn
}

func newOutgoingContext(ctx context.Context, channel *Channel, event *Event, user *User, conn *Conn) *OutgoingContext {
	select {
	case <-ctx.Done():
		return &OutgoingContext{
			Channel: channel,
			Event:   event,
			User:    user,
			conn:    conn,
			ctx:     ctx,
			err:     ctx.Err(),
		}
	default:
		return &OutgoingContext{
			Channel: channel,
			Event:   event,
			User:    user,
			ctx:     ctx,
			conn:    conn,
		}
	}
}

func (c *OutgoingContext) checkStateAndContext() bool {
	if c.err != nil {
		return true
	}
	select {
	case <-c.ctx.Done():
		c.err = c.ctx.Err()

		return true
	default:
		return false
	}
}

// Block prevents this message from being sent to the client.
// Once blocked, the message will be silently dropped.
// Returns the OutgoingContext for method chaining.
func (c *OutgoingContext) Block() *OutgoingContext {
	if c.checkStateAndContext() {
		return c
	}
	c.mutex.Lock()

	defer c.mutex.Unlock()

	c.isBlocked = true
	return c
}

// Unblock reverses a previous Block() call, allowing the message to be sent.
// Returns the OutgoingContext for method chaining.
func (c *OutgoingContext) Unblock() *OutgoingContext {
	if c.checkStateAndContext() {
		return c
	}
	c.mutex.Lock()

	defer c.mutex.Unlock()

	c.isBlocked = false
	return c
}

// IsBlocked returns true if the message has been blocked from sending.
// This method is thread-safe.
func (c *OutgoingContext) IsBlocked() bool {
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	return c.isBlocked
}

// Transform replaces the message payload with new data.
// This allows modifying the content before it reaches the client.
// The transformation is marked so subsequent handlers know the payload has changed.
// Returns the OutgoingContext for method chaining.
func (c *OutgoingContext) Transform(payload interface{}) *OutgoingContext {
	if c.checkStateAndContext() {
		return c
	}
	c.mutex.Lock()

	defer c.mutex.Unlock()

	c.Event.Payload = payload
	c.hasTransformed = true
	return c
}

// HasTransformed returns true if the payload has been transformed.
// This method is thread-safe.
func (c *OutgoingContext) HasTransformed() bool {
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	return c.hasTransformed
}

// RefreshUser fetches the latest user data from the channel.
// This ensures the User field contains up-to-date assigns and presence information.
// Useful when user data may have changed during message processing.
// Returns the OutgoingContext for method chaining.
func (c *OutgoingContext) RefreshUser() *OutgoingContext {
	if c.checkStateAndContext() {
		return c
	}
	user, err := c.Channel.GetUser(c.User.UserID)

	if err != nil {
		c.err = err
		return c
	}
	c.User = user
	return c
}

// GetPayload returns the current message payload.
// If Transform has been called, this returns the transformed payload.
// This method is thread-safe.
func (c *OutgoingContext) GetPayload() interface{} {
	c.mutex.RLock()

	defer c.mutex.RUnlock()

	return c.Event.Payload
}

// GetEvent returns the event name of the outgoing message.
// This identifies what type of event is being sent to the client.
func (c *OutgoingContext) GetEvent() string {
	return c.Event.Event
}

// Context returns the context for this outgoing message operation.
// The context is cancelled if the operation times out or the server shuts down.
func (c *OutgoingContext) Context() context.Context {
	return c.ctx
}

func (c *OutgoingContext) sendMessage() error {
	if c.checkStateAndContext() {
		return c.err
	}
	c.mutex.RLock()

	isBlocked := c.isBlocked
	c.mutex.RUnlock()

	if isBlocked {
		return nil
	}
	if c.conn != nil {
		return c.conn.sendJSON(c.Event)
	}
	return internal(c.Channel.name, "Cannot send message: no connection available")
}

func (c *OutgoingContext) setRoute(route *Route) {
	c.mutex.Lock()

	defer c.mutex.Unlock()

	c.Route = route
}

// Error implements the error interface for OutgoingContext.
// Returns the string representation of any error that occurred during outgoing context operations.
func (c *OutgoingContext) Error() string {
	if c.err != nil {
		return c.err.Error()
	}
	return ""
}
