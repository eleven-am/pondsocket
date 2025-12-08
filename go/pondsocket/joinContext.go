// This file contains the JoinContext struct which provides the interface for handling
// channel join requests. It allows accepting or declining join requests, setting initial
// presence, and configuring the user's state in the channel.
package pondsocket

import (
	"context"
	"errors"
)

type JoinContext struct {
	Channel      *Channel
	Route        *Route
	HasResponded bool
	conn         *Conn
	event        *Event
	ctx          context.Context
	accepted     bool
	err          error
}

func newJoinContext(ctx context.Context, channel *Channel, route *Route, user *Conn, ev *Event) *JoinContext {
	select {
	case <-ctx.Done():
		return &JoinContext{
			Channel: channel,
			Route:   route,
			conn:    user,
			event:   ev,
			ctx:     ctx,
			err:     ctx.Err(),
		}
	default:
		return &JoinContext{
			Channel: channel,
			Route:   route,
			conn:    user,
			event:   ev,
			ctx:     ctx,
		}
	}
}

func (c *JoinContext) checkStateAndContext() bool {
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

// Accept approves the user's request to join the channel.
// This adds the user to the channel and sends an acknowledgment event.
// Once accepted, the user can send and receive messages in the channel.
// Returns the JoinContext for method chaining.
func (c *JoinContext) Accept() *JoinContext {
	if c.checkStateAndContext() {
		return c
	}

	if c.HasResponded {
		c.err = badRequest(c.Channel.name, "Already responded to the join request")
		return c
	}

	c.HasResponded = true
	c.accepted = true
	if err := c.Channel.addUser(c.conn); err != nil {
		c.err = wrapF(err, "failed to add user %s to Channel %s", c.conn.ID, c.Channel.name)
		return c
	}

	ackEvent := Event{
		Action:      system,
		ChannelName: c.Channel.name,
		RequestId:   c.event.RequestId,
		Event:       string(acknowledgeEvent),
		Payload:     make(map[string]interface{}),
	}

	recp := recipients{userIds: []string{c.conn.ID}}
	if err := c.Channel.sendMessage(string(channelEntity), recp, ackEvent); err != nil {
		c.err = wrapF(err, "failed to send join acknowledgment for Channel %s", c.Channel.name)
	}

	return c
}

// Decline rejects the user's request to join the channel.
// Sends an unauthorized event to the client with the specified status code and message.
// After declining, the user is not added to the channel and cannot participate.
// Common status codes: 401 (Unauthorized), 403 (Forbidden).
func (c *JoinContext) Decline(statusCode int, message string) error {
	if c.checkStateAndContext() {
		return c.err
	}
	if c.HasResponded {
		return badRequest(c.Channel.name, "Already responded to the join request")
	}
	c.HasResponded = true
	c.accepted = false
	declineEvent := Event{
		Action:      system,
		ChannelName: c.Channel.name,
		RequestId:   c.event.RequestId,
		Event:       string(unauthorizedEvent),
		Payload: unauthorized(c.Channel.name, message).
			withDetails(map[string]int{
				"statusCode": statusCode,
			}),
	}
	return c.conn.sendJSON(declineEvent)
}

// Reply sends a system event to the joining user after accepting their join request.
// This can be used to send initial channel state, configuration, or welcome messages.
// The join request must be accepted before calling Reply.
// Returns the JoinContext for method chaining.
func (c *JoinContext) Reply(e string, payload interface{}) *JoinContext {
	if c.checkStateAndContext() {
		return c
	}

	if !c.accepted {
		c.err = badRequest(c.Channel.name, "Cannot reply before accepting join request")
		return c
	}

	replyEvent := Event{
		Action:      system,
		ChannelName: c.Channel.name,
		RequestId:   c.event.RequestId,
		Event:       e,
		Payload:     payload,
	}

	recp := recipients{userIds: []string{c.conn.ID}}
	if err := c.Channel.sendMessage(string(channelEntity), recp, replyEvent); err != nil {
		c.err = wrapF(err, "failed to send reply message '%s' for Channel %s", e, c.Channel.name)
	}

	return c
}

// SetAssigns sets a key-value pair in the user's assigns for this channel.
// If called before Accept, it updates the connection's assigns.
// If called after Accept, it updates the user's assigns in the channel.
// Assigns are server-side metadata and are never sent to clients.
// Returns the JoinContext for method chaining.
func (c *JoinContext) SetAssigns(key string, value interface{}) *JoinContext {
	if c.checkStateAndContext() {
		return c
	}
	if !c.accepted {
		c.conn.setAssign(key, value)
	} else {
		err := c.Channel.UpdateAssigns(c.conn.ID, key, value)

		if err != nil {
			c.err = wrapF(err, "error setting assign '%s' for user %s", key, c.conn.ID)
		}
	}
	return c
}

// Assigns sets multiple key-value pairs in the user's assigns for this channel.
// If called before Accept, it updates the connection's assigns.
// If called after Accept, it updates the user's assigns in the channel.
// Assigns are server-side metadata and are never sent to clients.
// Returns the JoinContext for method chaining.
func (c *JoinContext) Assigns(assigns interface{}) *JoinContext {
	if c.checkStateAndContext() {
		return c
	}

	mapAssigns, ok := assigns.(map[string]interface{})
	if !ok {
		c.err = badRequest(c.Channel.name, "Assigns must be a map[string]interface{}")

		return c
	}

	for key, value := range mapAssigns {
		c.SetAssigns(key, value)
	}

	return c
}

// GetAssigns retrieves a value from the user's assigns by key.
// If called before Accept, it reads from the connection's assigns.
// If called after Accept, it reads from the user's channel assigns.
// Returns nil if the key doesn't exist.
func (c *JoinContext) GetAssigns(key string) interface{} {
	if c.checkStateAndContext() {
		return nil
	}

	var value interface{}
	if !c.accepted {
		value = c.conn.GetAssign(key)
	} else {
		assignValue, err := c.Channel.getUserAssigns(c.conn.ID, key)

		if err != nil {
			var pondErr *Error
			if !errors.As(err, &pondErr) || pondErr.Code != StatusNotFound {
				c.err = wrapF(err, "failed to get assign '%s' for user %s", key, c.conn.ID)
			}
		}
		value = assignValue
	}
	return value
}

// Broadcast sends an event to all users in the channel.
// The join request must be accepted before broadcasting.
// This is useful for notifying other users that someone has joined.
// Returns the JoinContext for method chaining.
func (c *JoinContext) Broadcast(e string, payload interface{}) *JoinContext {
	if c.checkStateAndContext() {
		return c
	}
	if !c.accepted {
		c.err = badRequest(c.Channel.name, "Cannot broadcast before accepting join request")

		return c
	}
	if err := c.Channel.Broadcast(e, payload); err != nil {
		c.err = wrapF(err, "error broadcasting event %s to all users", e)
	}
	return c
}

// BroadcastTo sends an event to specific users in the channel.
// The join request must be accepted before broadcasting.
// Only the specified users will receive the message.
// Returns the JoinContext for method chaining.
func (c *JoinContext) BroadcastTo(e string, payload interface{}, userIDs ...string) *JoinContext {
	if c.checkStateAndContext() {
		return c
	}
	if !c.accepted {
		c.err = badRequest(c.Channel.name, "Cannot broadcast before accepting join request")

		return c
	}
	if err := c.Channel.BroadcastTo(e, payload, userIDs...); err != nil {
		c.err = wrapF(err, "error broadcasting event %s to users %v", e, userIDs)
	}
	return c
}

// BroadcastFrom sends an event to all users except the joining user.
// The join request must be accepted before broadcasting.
// This is commonly used to notify others of a new user joining.
// Returns the JoinContext for method chaining.
func (c *JoinContext) BroadcastFrom(e string, payload interface{}) *JoinContext {
	if c.checkStateAndContext() {
		return c
	}
	if !c.accepted {
		c.err = badRequest(c.Channel.name, "Cannot broadcast before accepting join request")

		return c
	}
	if err := c.Channel.BroadcastFrom(e, payload, c.conn.ID); err != nil {
		c.err = wrapF(err, "error broadcasting event %s to all users except %s", e, c.conn.ID)
	}
	return c
}

// Track starts tracking presence for the joining user with the given data.
// The join request must be accepted before tracking presence.
// Once tracked, the user will receive presence updates from other tracked users.
// Returns the JoinContext for method chaining.
func (c *JoinContext) Track(presence interface{}) *JoinContext {
	if c.checkStateAndContext() {
		return c
	}
	if !c.accepted {
		c.err = badRequest(c.Channel.name, "Cannot track presence before accepting join request")

		return c
	}
	if err := c.Channel.Track(c.conn.ID, presence); err != nil {
		c.err = wrapF(err, "error tracking presence for user %s", c.conn.ID)
	}
	return c
}

// GetPayload returns the payload sent with the join request.
// This can contain authentication tokens, initial state, or other join parameters.
func (c *JoinContext) GetPayload() interface{} {
	return c.event.Payload
}

// ParsePayload unmarshals the join request payload into the provided struct.
// This is useful for deserializing structured join parameters.
// Returns an error if the payload cannot be parsed into the target type.
func (c *JoinContext) ParsePayload(v interface{}) error {
	if c.checkStateAndContext() {
		return c.err
	}
	return parsePayload(v, c.event.Payload)
}

// ParseAssigns unmarshals the joining user's assigns into the provided struct.
// If called before Accept, reads from connection assigns. If called after Accept, reads from channel assigns.
// This is useful for deserializing user assigns into typed structs.
// Returns an error if the assigns cannot be parsed into the target type.
func (c *JoinContext) ParseAssigns(v interface{}) error {
	if c.checkStateAndContext() {
		return c.err
	}
	user := c.GetUser()
	if user == nil {
		return wrapF(nil, "user not found")
	}
	return parseAssigns(v, user.Assigns)
}

// ParsePresence unmarshals the joining user's presence data into the provided struct.
// This is useful for deserializing user presence into typed structs.
// Returns an error if the presence cannot be parsed into the target type or if user has no presence.
func (c *JoinContext) ParsePresence(v interface{}) error {
	if c.checkStateAndContext() {
		return c.err
	}
	user := c.GetUser()
	if user == nil {
		return wrapF(nil, "user not found")
	}
	return parsePresence(v, user.Presence)
}

// GetAllPresence returns a map of all tracked users' presence data in the channel.
// Automatically handles distribution if PubSub is configured.
// Returns nil if there's an error or the channel is shutting down.
func (c *JoinContext) GetAllPresence() map[string]interface{} {
	if c.checkStateAndContext() {
		return nil
	}
	if c.Channel == nil {
		return nil
	}
	return c.Channel.GetPresence()
}

// GetAllAssigns returns a map of all users' assigns data in the channel.
// Automatically handles distribution if PubSub is configured.
// Returns nil if there's an error or the channel is shutting down.
func (c *JoinContext) GetAllAssigns() map[string]map[string]interface{} {
	if c.checkStateAndContext() {
		return nil
	}
	if c.Channel == nil {
		return nil
	}
	return c.Channel.GetAssigns()
}

// GetUser returns a User struct representing the joining user.
// If called before Accept, returns basic user info from the connection.
// If called after Accept, returns full user info including channel-specific data.
func (c *JoinContext) GetUser() *User {
	user := &User{
		UserID:   c.conn.ID,
		Assigns:  c.conn.assigns,
		Presence: nil,
	}

	if c.checkStateAndContext() {
		return user
	}

	if !c.accepted {
		return user
	}

	newUser, err := c.Channel.GetUser(c.conn.ID)

	if err != nil {
		c.err = wrapF(err, "error getting user %s from Channel %s", c.conn.ID, c.Channel.name)

		return user
	}
	return newUser
}

// Context returns the context for this join operation.
// The context is cancelled if the operation times out or the server shuts down.
func (c *JoinContext) Context() context.Context {
	return c.ctx
}

// Error implements the error interface for JoinContext.
// Returns the string representation of any error that occurred during join context operations.
func (c *JoinContext) Error() string {
	if c.err != nil {
		return c.err.Error()
	}
	return ""
}
