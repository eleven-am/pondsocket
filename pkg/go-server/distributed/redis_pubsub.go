// Package distributed provides distributed PubSub implementations for PondSocket.
// This package includes adapters for various message brokers to enable
// multi-node deployments of PondSocket servers.
package distributed

import (
	"context"
	"fmt"
	"sync"

	"github.com/redis/go-redis/v9"
)

// RedisPubSub implements the PondSocket PubSub interface using Redis.
// It enables PondSocket servers to communicate across multiple nodes
// by leveraging Redis's publish-subscribe functionality.
type RedisPubSub struct {
	client *redis.Client
	pubsub *redis.PubSub

	mu            sync.RWMutex
	subscriptions map[string][]func(topic string, data []byte)
	patterns      map[string]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	closed bool

	wg sync.WaitGroup
}

// NewRedisPubSub creates a new Redis-based PubSub implementation.
// The provided Redis client should be properly configured and connected.
func NewRedisPubSub(ctx context.Context, client *redis.Client) (*RedisPubSub, error) {
	// Test the connection
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	pubsubCtx, cancel := context.WithCancel(ctx)

	r := &RedisPubSub{
		client:        client,
		subscriptions: make(map[string][]func(topic string, data []byte)),
		patterns:      make(map[string]struct{}),
		ctx:           pubsubCtx,
		cancel:        cancel,
	}

	// Create a new PubSub instance
	r.pubsub = client.Subscribe(pubsubCtx)

	// Start the message handler
	r.wg.Add(1)
	go r.handleMessages()

	return r, nil
}

// Subscribe registers a handler for messages matching the given pattern.
// Redis pattern matching supports wildcards: * for any characters.
func (r *RedisPubSub) Subscribe(pattern string, handler func(topic string, data []byte)) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return fmt.Errorf("pubsub: closed")
	}

	// Convert PondSocket pattern to Redis pattern
	redisPattern := convertToRedisPattern(pattern)

	// Subscribe to the pattern if not already subscribed
	if _, exists := r.patterns[redisPattern]; !exists {
		if err := r.pubsub.PSubscribe(r.ctx, redisPattern); err != nil {
			return fmt.Errorf("failed to subscribe to pattern %s: %w", pattern, err)
		}
		r.patterns[redisPattern] = struct{}{}
	}

	// Add the handler
	r.subscriptions[pattern] = append(r.subscriptions[pattern], handler)

	return nil
}

// Unsubscribe removes all handlers for the given pattern.
func (r *RedisPubSub) Unsubscribe(pattern string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return fmt.Errorf("pubsub: closed")
	}

	// Remove handlers
	delete(r.subscriptions, pattern)

	// Check if any other subscriptions use this Redis pattern
	redisPattern := convertToRedisPattern(pattern)
	stillNeeded := false
	for p := range r.subscriptions {
		if convertToRedisPattern(p) == redisPattern {
			stillNeeded = true
			break
		}
	}

	// Unsubscribe from Redis if pattern is no longer needed
	if !stillNeeded {
		if err := r.pubsub.PUnsubscribe(r.ctx, redisPattern); err != nil {
			return fmt.Errorf("failed to unsubscribe from pattern %s: %w", pattern, err)
		}
		delete(r.patterns, redisPattern)
	}

	return nil
}

// Publish sends a message to the specified topic.
func (r *RedisPubSub) Publish(topic string, data []byte) error {
	r.mu.RLock()
	closed := r.closed
	r.mu.RUnlock()

	if closed {
		return fmt.Errorf("pubsub: closed")
	}

	if err := r.client.Publish(r.ctx, topic, data).Err(); err != nil {
		return fmt.Errorf("failed to publish message: %w", err)
	}

	return nil
}

// Close shuts down the Redis PubSub connection and cleans up resources.
func (r *RedisPubSub) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	r.mu.Unlock()

	// Cancel context to stop message handler
	r.cancel()

	// Close the PubSub connection
	if err := r.pubsub.Close(); err != nil {
		return fmt.Errorf("failed to close pubsub: %w", err)
	}

	// Wait for message handler to finish
	r.wg.Wait()

	return nil
}

// handleMessages processes incoming messages from Redis.
func (r *RedisPubSub) handleMessages() {
	defer r.wg.Done()

	ch := r.pubsub.Channel()

	for {
		select {
		case <-r.ctx.Done():
			return

		case msg, ok := <-ch:
			if !ok {
				return
			}

			// Redis v9 returns Message directly, not as interface
			if msg.Payload != "" {
				r.deliverMessage(msg.Channel, []byte(msg.Payload))
			}
		}
	}
}

// deliverMessage distributes a message to all matching handlers.
func (r *RedisPubSub) deliverMessage(topic string, data []byte) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Find all matching subscriptions
	for pattern, handlers := range r.subscriptions {
		if matchPattern(pattern, topic) {
			// Call each handler in a goroutine to prevent blocking
			for _, handler := range handlers {
				h := handler // Capture for goroutine
				go func() {
					// Recover from panics in handlers
					defer func() {
						if r := recover(); r != nil {
							// In production, log the panic
						}
					}()
					h(topic, data)
				}()
			}
		}
	}
}

// convertToRedisPattern converts PondSocket patterns to Redis patterns.
// PondSocket uses .* for wildcards, Redis uses *
func convertToRedisPattern(pattern string) string {
	// Replace .* with * for Redis
	if len(pattern) > 2 && pattern[len(pattern)-2:] == ".*" {
		return pattern[:len(pattern)-2] + "*"
	}
	return pattern
}

// matchPattern checks if a topic matches a PondSocket pattern.
func matchPattern(pattern, topic string) bool {
	// Exact match
	if pattern == topic {
		return true
	}

	// Wildcard match
	if len(pattern) > 2 && pattern[len(pattern)-2:] == ".*" {
		prefix := pattern[:len(pattern)-2]
		return len(topic) >= len(prefix) && topic[:len(prefix)] == prefix
	}

	return false
}
