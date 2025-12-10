// This file contains the LeaveContext struct which provides the interface for handling
// user departure from channels. It allows broadcasting leave notifications, accessing user data,
// and performing cleanup operations when a user leaves.
package pondsocket

import (
	"context"
	"sync"
)

type LeaveContext struct {
	Channel      *Channel
	Route        *Route
	user         *User
	ctx          context.Context
	err          error
	mutex        sync.RWMutex
	hasResponded bool
	leaveReason  string
}

func newLeaveContext(ctx context.Context, channel *Channel, user *User, reason string) *LeaveContext {
	select {
	case <-ctx.Done():
		return &LeaveContext{
			Channel:     channel,
			user:        user,
			ctx:         ctx,
			err:         ctx.Err(),
			leaveReason: reason,
		}
	default:
		return &LeaveContext{
			Channel:     channel,
			user:        user,
			ctx:         ctx,
			leaveReason: reason,
		}
	}
}

func (c *LeaveContext) checkStateAndContext() bool {
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

// Broadcast sends a leave notification to all remaining users in the channel.
// This is commonly used to notify others that a user has left.
// The payload can contain information about who left and why.
// Returns the LeaveContext for method chaining.
func (c *LeaveContext) Broadcast(e string, payload interface{}) *LeaveContext {
	if c.checkStateAndContext() {
		return c
	}

	c.mutex.Lock()
	if c.hasResponded {
		c.err = badRequest(c.Channel.name, "Already broadcast leave notification")
		c.mutex.Unlock()
		return c
	}
	c.hasResponded = true
	c.mutex.Unlock()

	if err := c.Channel.Broadcast(e, payload); err != nil {
		c.err = wrapF(err, "error broadcasting leave event %s", e)
	}
	return c
}

// BroadcastTo sends a leave notification to specific users in the channel.
// Only the users whose IDs are provided will receive the notification.
// Returns the LeaveContext for method chaining.
func (c *LeaveContext) BroadcastTo(e string, payload interface{}, userIDs ...string) *LeaveContext {
	if c.checkStateAndContext() {
		return c
	}

	c.mutex.Lock()
	if c.hasResponded {
		c.err = badRequest(c.Channel.name, "Already broadcast leave notification")
		c.mutex.Unlock()
		return c
	}
	c.hasResponded = true
	c.mutex.Unlock()

	if err := c.Channel.BroadcastTo(e, payload, userIDs...); err != nil {
		c.err = wrapF(err, "error broadcasting leave event %s to users %v", e, userIDs)
	}
	return c
}

// GetReason returns the reason why the user is leaving the channel.
// This could be "disconnect", "evicted", "explicit_leave", or a custom reason.
func (c *LeaveContext) GetReason() string {
	return c.leaveReason
}

// GetAllPresence returns a map of all tracked users' presence data in the channel.
// This excludes the leaving user's presence as it has already been removed.
// Automatically handles distribution if PubSub is configured.
func (c *LeaveContext) GetAllPresence() map[string]interface{} {
	if c.checkStateAndContext() {
		return nil
	}
	if c.Channel == nil {
		return nil
	}
	return c.Channel.GetPresence()
}

// GetAllAssigns returns a map of all users' assigns data in the channel.
// This excludes the leaving user's assigns as they have already been removed.
// Automatically handles distribution if PubSub is configured.
func (c *LeaveContext) GetAllAssigns() map[string]map[string]interface{} {
	if c.checkStateAndContext() {
		return nil
	}
	if c.Channel == nil {
		return nil
	}
	return c.Channel.GetAssigns()
}

// RemainingUserCount returns the number of users still in the channel after this user leaves.
func (c *LeaveContext) RemainingUserCount() int {
	if c.checkStateAndContext() {
		return 0
	}
	return c.Channel.store.Len()
}

// ParseAssigns unmarshals the leaving user's assigns into the provided struct.
// This is useful for accessing the user's metadata before they fully leave.
// Returns an error if the assigns cannot be parsed into the target type.
func (c *LeaveContext) ParseAssigns(v interface{}) error {
	if c.checkStateAndContext() {
		return c.err
	}
	if c.user == nil {
		return wrapF(nil, "user not found")
	}
	return parseAssigns(v, c.user.Assigns)
}

// ParsePresence unmarshals the leaving user's presence data into the provided struct.
// This is useful for accessing the user's last known state before they fully leave.
// Returns an error if the presence cannot be parsed into the target type or if user has no presence.
func (c *LeaveContext) ParsePresence(v interface{}) error {
	if c.checkStateAndContext() {
		return c.err
	}
	if c.user == nil {
		return wrapF(nil, "user not found")
	}
	return parsePresence(v, c.user.Presence)
}

func (c *LeaveContext) GetUser() *User {
	return c.user
}

func (c *LeaveContext) GetAssign(key string) interface{} {
	if c.user == nil || c.user.Assigns == nil {
		return nil
	}
	return c.user.Assigns[key]
}

func (c *LeaveContext) Context() context.Context {
	return c.ctx
}

// Error implements the error interface for LeaveContext.
// Returns the string representation of any error that occurred during leave context operations.
func (c *LeaveContext) Error() string {
	if c.err != nil {
		return c.err.Error()
	}
	return ""
}
