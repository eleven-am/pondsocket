// This file contains the EventContext struct which provides the interface for handling
// messages within a channel. It allows replying to messages, broadcasting events,
// managing presence, and updating user state.
package main

import (
	"context"
	"errors"
	"sync"
)

type EventContext struct {
	Channel      *Channel
	Route        *Route
	HasResponded bool
	user         *User
	event        *Event
	ctx          context.Context
	err          error
}

func newEventContext(ctx context.Context, channel *Channel, request *messageEvent, route *Route) *EventContext {
	select {
	case <-ctx.Done():
		return &EventContext{
			Channel: channel,
			user:    request.User,
			event:   request.Event,
			Route:   route,
			ctx:     ctx,
			err:     ctx.Err(),
		}
	default:
		return &EventContext{
			Channel: channel,
			user:    request.User,
			event:   request.Event,
			Route:   route,
			ctx:     ctx,
		}
	}
}

func (c *EventContext) checkStateAndContext() bool {
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

// Reply sends a response event back to the message sender.
// Only one reply can be sent per message (HasResponded prevents multiple replies).
// The reply is sent as a system event to maintain the request-response correlation.
// Returns the EventContext for method chaining.
func (c *EventContext) Reply(e string, payload interface{}) *EventContext {
	if c.checkStateAndContext() {
		return c
	}
	if c.HasResponded {
		c.err = badRequest(c.Channel.name, "Already responded to event "+c.event.RequestId)

		return c
	}
	c.HasResponded = true
	response := Event{
		Action:      system,
		ChannelName: c.Channel.name,
		RequestId:   c.event.RequestId,
		Event:       e,
		Payload:     payload,
	}
	recp := recipients{userIds: []string{c.user.UserID}}
	if err := c.Channel.sendMessage(string(channelEntity), recp, response); err != nil {
		c.err = wrapF(err, "failed to send reply '%s' for event %s", e, c.event.RequestId)
	}
	return c
}

// SetAssigns updates a key-value pair in the sender's channel assigns.
// Assigns are server-side metadata that persist with the user in this channel.
// These values are never automatically sent to clients.
// Returns the EventContext for method chaining.
func (c *EventContext) SetAssigns(key string, value interface{}) *EventContext {
	if c.checkStateAndContext() {
		return c
	}
	err := c.Channel.UpdateAssigns(c.user.UserID, key, value)

	if err != nil {
		c.err = wrapF(err, "error setting assign '%s' for user %s", key, c.user.UserID)
	}
	return c
}

// Track starts tracking presence for one or more users.
// If no userIds are provided, tracks presence for the message sender.
// Once tracked, users receive presence updates from other tracked users.
// Returns the EventContext for method chaining.
func (c *EventContext) Track(presence interface{}, userIds ...string) *EventContext {
	targetUserDesc := "current user"
	if len(userIds) > 0 {
		targetUserDesc = "specified users"
	}
	if c.checkStateAndContext() {
		return c
	}
	err := c.routines(userIds, func(ctx context.Context, ch *Channel, userId string) error {
		return ch.Track(userId, presence)
	})

	if err != nil {
		c.err = wrapF(err, "error tracking presence for %s", targetUserDesc)
	}
	return c
}

// Update changes the presence data for one or more tracked users.
// If no userIds are provided, updates presence for the message sender.
// The user must already be tracked for the update to succeed.
// Returns the EventContext for method chaining.
func (c *EventContext) Update(presence interface{}, userIds ...string) *EventContext {
	targetUserDesc := "current user"
	if len(userIds) > 0 {
		targetUserDesc = "specified users"
	}
	if c.checkStateAndContext() {
		return c
	}
	err := c.routines(userIds, func(ctx context.Context, ch *Channel, userId string) error {
		return ch.UpdatePresence(userId, presence)
	})

	if err != nil {
		c.err = wrapF(err, "error updating presence for %s", targetUserDesc)
	}
	return c
}

// UnTrack stops tracking presence for one or more users.
// If no userIds are provided, stops tracking for the message sender.
// After untracking, users no longer receive presence updates.
// Returns the EventContext for method chaining.
func (c *EventContext) UnTrack(userIds ...string) *EventContext {
	targetUserDesc := "current user"
	if len(userIds) > 0 {
		targetUserDesc = "specified users"
	}
	if c.checkStateAndContext() {
		return c
	}
	err := c.routines(userIds, func(ctx context.Context, ch *Channel, userId string) error {
		return ch.UnTrack(userId)
	})

	if err != nil {
		c.err = wrapF(err, "error untracking presence for %s", targetUserDesc)
	}
	return c
}

// Broadcast sends an event to all users in the channel.
// The event is delivered to every connected user including the sender.
// Returns the EventContext for method chaining.
func (c *EventContext) Broadcast(e string, payload interface{}) *EventContext {
	if c.checkStateAndContext() {
		return c
	}
	if err := c.Channel.Broadcast(e, payload); err != nil {
		c.err = wrapF(err, "error broadcasting event %s to all users", e)
	}
	return c
}

// BroadcastTo sends an event to specific users in the channel.
// Only the users whose IDs are provided will receive the message.
// Returns the EventContext for method chaining.
func (c *EventContext) BroadcastTo(e string, payload interface{}, userIDs ...string) *EventContext {
	if c.checkStateAndContext() {
		return c
	}
	if err := c.Channel.BroadcastTo(e, payload, userIDs...); err != nil {
		c.err = wrapF(err, "error broadcasting event %s to users %v", e, userIDs)
	}
	return c
}

// BroadcastFrom sends an event to all users except the message sender.
// This is useful for events like "user is typing" where the sender doesn't need the notification.
// Returns the EventContext for method chaining.
func (c *EventContext) BroadcastFrom(e string, payload interface{}) *EventContext {
	if c.checkStateAndContext() {
		return c
	}
	if err := c.Channel.BroadcastFrom(e, payload, c.user.UserID); err != nil {
		c.err = wrapF(err, "error broadcasting event %s to all users except %s", e, c.user.UserID)
	}
	return c
}

// Evict forcefully removes one or more users from the channel.
// If no userIds are provided, evicts the message sender.
// Evicted users receive an eviction notification and are removed from the channel.
// Returns the EventContext for method chaining.
func (c *EventContext) Evict(reason string, userIds ...string) *EventContext {
	targetUserDesc := "current user"
	if len(userIds) > 0 {
		targetUserDesc = "specified users"
	}
	if c.checkStateAndContext() {
		return c
	}
	err := c.routines(userIds, func(ctx context.Context, ch *Channel, userId string) error {
		return ch.EvictUser(userId, reason)
	})

	if err != nil {
		c.err = wrapF(err, "error evicting %s", targetUserDesc)
	}
	return c
}

// Err returns any error that occurred during event context operations.
// This should be checked after method chaining to handle any errors.
func (c *EventContext) Err() error {
	return c.err
}

// GetPayload returns the payload from the received message event.
// This contains the data sent by the client with the message.
func (c *EventContext) GetPayload() interface{} {
	return c.event.Payload
}

// ParsePayload unmarshals the message payload into the provided struct.
// This is useful for deserializing structured message data.
// Returns an error if the payload cannot be parsed into the target type.
func (c *EventContext) ParsePayload(v interface{}) error {
	if c.checkStateAndContext() {
		return c.err
	}
	return parsePayload(v, c.event.Payload)
}

// GetUser returns the User struct for the message sender.
// This includes the user's current assigns and presence data in the channel.
// The returned user data is fetched fresh from the channel state.
func (c *EventContext) GetUser() *User {
	if c.checkStateAndContext() {
		return c.user
	}
	user, err := c.Channel.GetUser(c.user.UserID)

	if err != nil {
		c.err = err
		return c.user
	}
	return user
}

// GetAssign retrieves a specific assign value for the message sender by key.
// Returns the value and nil error if found, or nil and an error if not found.
// Assigns are server-side metadata stored per user in the channel.
func (c *EventContext) GetAssign(key string) (interface{}, error) {
	if c.checkStateAndContext() {
		return nil, c.err
	}
	value, err := c.Channel.getUserAssigns(c.user.UserID, key)

	if err != nil {
		var pondErr *Error
		if !errors.As(err, &pondErr) || pondErr.Code != StatusNotFound {
			c.err = wrapF(err, "failed to get assign '%s' for user %s", key, c.user.UserID)
		}
		return nil, err
	}
	return value, nil
}

// SetAssign is an alias for SetAssigns, updating a single key-value pair.
// Returns the EventContext for method chaining.
func (c *EventContext) SetAssign(key string, value interface{}) *EventContext {
	return c.SetAssigns(key, value)
}

func (c *EventContext) routines(userIds []string, handler func(ctx context.Context, ch *Channel, userId string) error) error {
	idsToProcess := userIds
	if len(idsToProcess) == 0 {
		idsToProcess = []string{c.user.UserID}
	}

	var wg sync.WaitGroup

	var mu sync.Mutex

	var allErrors error
	for _, id := range idsToProcess {
		select {
		case <-c.ctx.Done():
			allErrors = addError(allErrors, c.ctx.Err())

			continue
		default:
		}
		wg.Add(1)

		go func(userId string) {
			defer wg.Done()

			err := handler(c.ctx, c.Channel, userId)

			if err != nil {
				select {
				case <-c.ctx.Done():
					err = c.ctx.Err()

				default:
				}
				mu.Lock()

				allErrors = addError(allErrors, err)

				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	return allErrors
}

// Context returns the context for this message event.
// The context is cancelled if the operation times out or the server shuts down.
func (c *EventContext) Context() context.Context {
	return c.ctx
}
