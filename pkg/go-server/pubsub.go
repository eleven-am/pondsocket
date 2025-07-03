// This file defines the PubSub interface and utilities for distributed messaging.
// PubSub enables PondSocket to work in distributed environments by synchronizing
// state and broadcasting messages across multiple server nodes.
package pondsocket

import (
	"errors"
	"fmt"
)

// PubSub defines the interface for publish-subscribe messaging systems.
// Implementations of this interface enable PondSocket to operate in distributed
// environments by coordinating state and messages across multiple nodes.
type PubSub interface {
	// Subscribe registers a handler for messages matching the given pattern.
	// Patterns can include wildcards (e.g., "channel.*.message").
	// The handler is called asynchronously when matching messages are received.
	Subscribe(pattern string, handler func(topic string, data []byte)) error

	// Unsubscribe removes all handlers for the given pattern.
	// Returns an error if the pattern was not previously subscribed.
	Unsubscribe(pattern string) error

	// Publish sends a message to the specified topic.
	// All subscribers with patterns matching the topic will receive the message.
	Publish(topic string, data []byte) error

	// Close shuts down the PubSub system and cleans up resources.
	Close() error
}

type PubSubMessage struct {
	Topic string
	Data  []byte
}

type pubsubClosedError struct{}

func (e *pubsubClosedError) Error() string {
	return "pubsub: closed"
}

func isPubSubClosed(err error) bool {

	var pubsubClosedError *pubsubClosedError
	ok := errors.As(err, &pubsubClosedError)

	return ok
}

func matchTopic(pattern, topic string) bool {
	if pattern == topic {
		return true
	}
	if len(pattern) > 2 && pattern[len(pattern)-2:] == ".*" {
		prefix := pattern[:len(pattern)-2]
		return len(topic) >= len(prefix) && topic[:len(prefix)] == prefix
	}
	return false
}

func formatTopic(endpoint, channel, event string) string {
	return fmt.Sprintf("pondsocket:%s:%s:%s", endpoint, channel, event)
}

func formatPresenceTopic(endpoint, channel string) string {
	return formatTopic(endpoint, channel, "presence")
}

func formatMessageTopic(endpoint, channel string) string {
	return formatTopic(endpoint, channel, "message")
}

func formatSystemTopic(event string) string {
	return fmt.Sprintf("pondsocket:system:%s", event)
}
