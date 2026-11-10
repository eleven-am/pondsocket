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
	subscriptions map[string][]*redisSubscription
	patterns      map[string]struct{}

	ctx    context.Context
	cancel context.CancelFunc
	closed bool

	wg sync.WaitGroup
}

type redisSubscription struct {
	handler func(topic string, data []byte)
}

// NewRedisPubSub creates a new Redis-based PubSub implementation.
// The provided Redis client should be properly configured and connected.
func NewRedisPubSub(ctx context.Context, client *redis.Client) (*RedisPubSub, error) {

	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("failed to connect to Redis: %w", err)
	}

	pubsubCtx, cancel := context.WithCancel(ctx)

	r := &RedisPubSub{
		client:        client,
		subscriptions: make(map[string][]*redisSubscription),
		patterns:      make(map[string]struct{}),
		ctx:           pubsubCtx,
		cancel:        cancel,
	}

	r.pubsub = client.Subscribe(pubsubCtx)

	r.wg.Add(1)
	go r.handleMessages()

	return r, nil
}

// Subscribe registers a handler for messages matching the given pattern.
// Redis pattern matching supports wildcards: * for any characters.
func (r *RedisPubSub) Subscribe(pattern string, handler func(topic string, data []byte)) error {
	_, err := r.SubscribeWithCancel(pattern, handler)
	return err
}

// SubscribeWithCancel registers one ordered handler and returns a function that
// removes only that handler without disturbing other subscribers to the pattern.
func (r *RedisPubSub) SubscribeWithCancel(pattern string, handler func(topic string, data []byte)) (func() error, error) {
	r.mu.Lock()

	if r.closed {
		r.mu.Unlock()
		return nil, fmt.Errorf("pubsub: closed")
	}

	redisPattern := convertToRedisPattern(pattern)

	if _, exists := r.patterns[redisPattern]; !exists {
		if err := r.pubsub.PSubscribe(r.ctx, redisPattern); err != nil {
			r.mu.Unlock()
			return nil, fmt.Errorf("failed to subscribe to pattern %s: %w", pattern, err)
		}
		r.patterns[redisPattern] = struct{}{}
	}

	sub := &redisSubscription{handler: handler}
	r.subscriptions[pattern] = append(r.subscriptions[pattern], sub)
	r.mu.Unlock()

	var once sync.Once
	unsubscribe := func() error {
		var err error
		once.Do(func() {
			err = r.removeSubscription(pattern, sub)
		})
		return err
	}
	return unsubscribe, nil
}

func (r *RedisPubSub) removeSubscription(pattern string, target *redisSubscription) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return fmt.Errorf("pubsub: closed")
	}
	subs := r.subscriptions[pattern]
	for i, sub := range subs {
		if sub == target {
			subs = append(subs[:i], subs[i+1:]...)
			break
		}
	}
	if len(subs) > 0 {
		r.subscriptions[pattern] = subs
		return nil
	}
	delete(r.subscriptions, pattern)
	return r.unsubscribeRedisPatternIfUnused(pattern)
}

func (r *RedisPubSub) unsubscribeRedisPatternIfUnused(pattern string) error {
	redisPattern := convertToRedisPattern(pattern)
	for p := range r.subscriptions {
		if convertToRedisPattern(p) == redisPattern {
			return nil
		}
	}
	if err := r.pubsub.PUnsubscribe(r.ctx, redisPattern); err != nil {
		return fmt.Errorf("failed to unsubscribe from pattern %s: %w", pattern, err)
	}
	delete(r.patterns, redisPattern)
	return nil
}

// Unsubscribe removes all handlers for the given pattern.
func (r *RedisPubSub) Unsubscribe(pattern string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.closed {
		return fmt.Errorf("pubsub: closed")
	}

	delete(r.subscriptions, pattern)
	return r.unsubscribeRedisPatternIfUnused(pattern)
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

	r.cancel()

	if err := r.pubsub.Close(); err != nil {
		return fmt.Errorf("failed to close pubsub: %w", err)
	}

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

			if msg.Payload != "" {
				r.deliverMessage(msg.Channel, []byte(msg.Payload))
			}
		}
	}
}

// deliverMessage distributes a message to all matching handlers.
func (r *RedisPubSub) deliverMessage(topic string, data []byte) {
	r.mu.RLock()
	var handlers []func(topic string, data []byte)
	for pattern, subscriptions := range r.subscriptions {
		if matchPattern(pattern, topic) {
			for _, subscription := range subscriptions {
				handlers = append(handlers, subscription.handler)
			}
		}
	}
	r.mu.RUnlock()

	for _, handler := range handlers {
		func() {
			defer func() { _ = recover() }()
			handler(topic, append([]byte(nil), data...))
		}()
	}
}

// convertToRedisPattern converts PondSocket patterns to Redis patterns.
// PondSocket uses .* for wildcards, Redis uses *
func convertToRedisPattern(pattern string) string {

	if len(pattern) > 2 && pattern[len(pattern)-2:] == ".*" {
		return pattern[:len(pattern)-2] + "*"
	}
	return pattern
}

// matchPattern checks if a topic matches a PondSocket pattern.
func matchPattern(pattern, topic string) bool {

	if pattern == topic {
		return true
	}

	if len(pattern) > 2 && pattern[len(pattern)-2:] == ".*" {
		prefix := pattern[:len(pattern)-2]
		return len(topic) >= len(prefix) && topic[:len(prefix)] == prefix
	}

	return false
}
