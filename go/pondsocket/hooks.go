// This file defines interfaces and implementations for extensibility hooks in PondSocket.
// It provides rate limiting, metrics collection, and lifecycle callbacks that can be
// integrated with external monitoring and control systems.
package pondsocket

import (
	"context"
	"time"
)

// RateLimiter defines the interface for rate limiting connections and operations.
// Implementations can enforce various rate limiting strategies per user, IP, or custom keys.
type RateLimiter interface {
	// Allow checks if an operation identified by key should be allowed.
	// Returns true if the operation is within rate limits, false if rate limited.
	// The key typically identifies a user, connection, or IP address.
	Allow(ctx context.Context, key string) (allowed bool, err error)

	// Reset clears the rate limit state for the given key.
	// This can be used to forgive rate limit violations or reset counters.
	Reset(key string)
}

// MetricsCollector defines the interface for collecting operational metrics.
// Implementations can forward these metrics to monitoring systems like Prometheus,
// StatsD, or custom analytics platforms.
type MetricsCollector interface {
	// ConnectionOpened is called when a new WebSocket connection is established.
	ConnectionOpened(connID string, endpoint string)

	// ConnectionClosed is called when a connection is closed, with the connection duration.
	ConnectionClosed(connID string, duration time.Duration)

	// ConnectionError is called when a connection encounters an error.
	ConnectionError(connID string, err error)

	// MessageReceived tracks incoming messages from clients.
	MessageReceived(connID string, channel string, event string, size int)

	// MessageSent tracks outgoing messages to clients.
	MessageSent(connID string, channel string, event string, size int)

	// MessageBroadcast tracks broadcast operations with recipient count.
	MessageBroadcast(channel string, event string, recipientCount int)

	// ChannelJoined is called when a user joins a channel.
	ChannelJoined(userID string, channel string)

	// ChannelLeft is called when a user leaves a channel.
	ChannelLeft(userID string, channel string)

	// ChannelCreated is called when a new channel is created.
	ChannelCreated(channel string)

	// ChannelDestroyed is called when a channel is destroyed.
	ChannelDestroyed(channel string)

	// HandlerDuration tracks the execution time of message handlers.
	HandlerDuration(handler string, duration time.Duration)

	// QueueDepth reports the current depth of internal queues.
	QueueDepth(queue string, depth int)

	// Error tracks errors occurring in different components.
	Error(component string, err error)
}

type Hooks struct {
	RateLimiter        RateLimiter
	ChannelRateLimiter RateLimiter
	Metrics            MetricsCollector
	OnConnect          func(conn Transport) error
	OnDisconnect       func(conn Transport)

	BeforeJoin func(user *User, channel string) error
	AfterJoin  func(user *User, channel string)

	BeforeMessage func(event *Event) error
	AfterMessage  func(event *Event, err error)
}

// WithRateLimiter creates a middleware that enforces rate limiting on incoming messages.
// The keyFunc extracts a rate limit key from the connection (e.g., user ID, IP address).
// Messages that exceed the rate limit are rejected with a 429 status code.
func WithRateLimiter(hooks *Hooks, keyFunc func(Transport) string) handlerFunc[*Event, interface{}] {
	return func(ctx context.Context, event *Event, _ interface{}, next nextFunc) error {
		if hooks == nil || hooks.RateLimiter == nil {
			return next()
		}
		conn, ok := ctx.Value(connectionContextKey).(Transport)

		if !ok {
			return next()
		}
		key := keyFunc(conn)

		allowed, err := hooks.RateLimiter.Allow(ctx, key)

		if err != nil {
			return wrapF(err, "rate limiter error")
		}
		if !allowed {
			if hooks.Metrics != nil {
				hooks.Metrics.Error("rate_limiter", &Error{
					Code:        StatusTooManyRequests,
					Message:     "Rate limit exceeded",
					ChannelName: event.ChannelName,
				})
			}
			return &Error{
				Code:        StatusTooManyRequests,
				Message:     "Rate limit exceeded",
				ChannelName: event.ChannelName,
			}
		}
		return next()
	}
}

// WithMetrics creates a middleware that collects metrics for message processing.
// It tracks message receipt, handler duration, and any errors that occur.
// The metrics are reported through the MetricsCollector interface.
func WithMetrics(hooks *Hooks) handlerFunc[*Event, interface{}] {
	return func(ctx context.Context, event *Event, _ interface{}, next nextFunc) error {
		if hooks == nil || hooks.Metrics == nil {
			return next()
		}
		start := time.Now()

		err := next()

		duration := time.Since(start)

		if conn, ok := ctx.Value(connectionContextKey).(Transport); ok {
			hooks.Metrics.MessageReceived(conn.GetID(), event.ChannelName, event.Event, 0)

			hooks.Metrics.HandlerDuration(event.Event, duration)

			if err != nil {
				hooks.Metrics.Error("message_handler", err)
			}
		}
		return err
	}
}

type noopMetrics struct{}

func (n *noopMetrics) ConnectionOpened(connID string, endpoint string) {}

func (n *noopMetrics) ConnectionClosed(connID string, duration time.Duration) {}

func (n *noopMetrics) ConnectionError(connID string, err error) {}

func (n *noopMetrics) MessageReceived(connID string, channel string, event string, size int) {}

func (n *noopMetrics) MessageSent(connID string, channel string, event string, size int) {}

func (n *noopMetrics) MessageBroadcast(channel string, event string, recipientCount int) {}

func (n *noopMetrics) ChannelJoined(userID string, channel string) {}

func (n *noopMetrics) ChannelLeft(userID string, channel string) {}

func (n *noopMetrics) ChannelCreated(channel string) {}

func (n *noopMetrics) ChannelDestroyed(channel string) {}

func (n *noopMetrics) HandlerDuration(handler string, duration time.Duration) {}

func (n *noopMetrics) QueueDepth(queue string, depth int) {}

func (n *noopMetrics) Error(component string, err error) {}

// NoopMetrics returns a no-operation metrics collector that discards all metrics.
// This is useful when you want to disable metrics collection without changing code structure.
func NoopMetrics() MetricsCollector {
	return &noopMetrics{}
}
