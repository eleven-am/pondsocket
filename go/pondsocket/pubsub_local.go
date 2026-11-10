// This file contains the LocalPubSub implementation which provides an in-memory
// publish-subscribe system for single-node deployments. It implements the PubSub
// interface using Go channels and is suitable for development and single-server setups.
package pondsocket

import (
	"context"
	"fmt"
	"sync"
)

type LocalPubSub struct {
	mu         sync.RWMutex
	subs       map[string][]*subscription
	closed     bool
	ctx        context.Context
	cancel     context.CancelFunc
	bufferSize int
}

type subscription struct {
	pattern string
	handler func(topic string, data []byte)

	ch     chan PubSubMessage
	cancel context.CancelFunc
}

// NewLocalPubSub creates a new local in-memory PubSub implementation.
// The bufferSize parameter sets the channel buffer size for each subscription.
// If bufferSize is <= 0, it defaults to 100.
// The context is used for lifecycle management of the PubSub system.
func NewLocalPubSub(ctx context.Context, bufferSize int) *LocalPubSub {
	if bufferSize <= 0 {
		bufferSize = 100
	}
	pubsubCtx, cancel := context.WithCancel(ctx)

	return &LocalPubSub{
		subs:       make(map[string][]*subscription),
		ctx:        pubsubCtx,
		cancel:     cancel,
		bufferSize: bufferSize,
	}
}

// Subscribe registers a handler for messages matching the given pattern.
// Multiple handlers can be registered for the same pattern.
// Each subscription runs in its own goroutine to prevent blocking.
// Returns an error if the PubSub system is closed.
func (l *LocalPubSub) Subscribe(pattern string, handler func(topic string, data []byte)) error {
	_, err := l.SubscribeWithCancel(pattern, handler)
	return err
}

// SubscribeWithCancel registers one ordered subscription and returns a function
// that removes only that subscription.
func (l *LocalPubSub) SubscribeWithCancel(pattern string, handler func(topic string, data []byte)) (func() error, error) {
	l.mu.Lock()

	if l.closed {
		l.mu.Unlock()
		return nil, &pubsubClosedError{}
	}
	subCtx, cancel := context.WithCancel(l.ctx)

	ch := make(chan PubSubMessage, l.bufferSize)

	sub := &subscription{
		pattern: pattern,
		handler: handler,
		ch:      ch,
		cancel:  cancel,
	}
	l.subs[pattern] = append(l.subs[pattern], sub)
	l.mu.Unlock()

	go l.runSubscription(subCtx, sub)

	var once sync.Once
	unsubscribe := func() error {
		var err error
		once.Do(func() {
			err = l.removeSubscription(pattern, sub)
		})
		return err
	}
	return unsubscribe, nil
}

func (l *LocalPubSub) runSubscription(ctx context.Context, sub *subscription) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-sub.ch:
			func() {
				defer func() { _ = recover() }()
				sub.handler(msg.Topic, msg.Data)
			}()
		}
	}
}

func (l *LocalPubSub) removeSubscription(pattern string, target *subscription) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return &pubsubClosedError{}
	}
	subs, exists := l.subs[pattern]
	if !exists {
		return nil
	}
	for i, sub := range subs {
		if sub != target {
			continue
		}
		sub.cancel()
		subs = append(subs[:i], subs[i+1:]...)
		if len(subs) == 0 {
			delete(l.subs, pattern)
		} else {
			l.subs[pattern] = subs
		}
		return nil
	}
	return nil
}

// Unsubscribe removes all handlers for the given pattern.
// It cancels the contexts and closes the channels for all subscriptions.
// Returns an error if the pattern was not subscribed or if the system is closed.
func (l *LocalPubSub) Unsubscribe(pattern string) error {
	l.mu.Lock()

	defer l.mu.Unlock()

	if l.closed {
		return &pubsubClosedError{}
	}
	subs, exists := l.subs[pattern]
	if !exists {
		return notFound("pubsub", "pattern not found")
	}
	for _, sub := range subs {
		sub.cancel()
	}
	delete(l.subs, pattern)

	return nil
}

// Publish sends a message to all subscribers whose patterns match the topic.
// The message is sent asynchronously to prevent blocking.
// If a subscriber's channel is full, the message is dropped for that subscriber.
// Returns an error if the PubSub system is closed.
func (l *LocalPubSub) Publish(topic string, data []byte) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return &pubsubClosedError{}
	}
	var matching []*subscription
	for pattern, subs := range l.subs {
		if matchTopic(pattern, topic) {
			matching = append(matching, subs...)
		}
	}
	for _, sub := range matching {
		if len(sub.ch) == cap(sub.ch) {
			return fmt.Errorf("pubsub: subscriber queue full for pattern %q", sub.pattern)
		}
	}
	for _, sub := range matching {
		messageData := append([]byte(nil), data...)
		sub.ch <- PubSubMessage{Topic: topic, Data: messageData}
	}
	return nil
}

// Close shuts down the LocalPubSub system.
// It cancels all subscription contexts and closes all channels.
// After closing, no new subscriptions or publishes are allowed.
// This method is idempotent and can be called multiple times safely.
func (l *LocalPubSub) Close() error {
	l.mu.Lock()

	defer l.mu.Unlock()

	if l.closed {
		return nil
	}
	l.closed = true
	l.cancel()

	for _, subs := range l.subs {
		for _, sub := range subs {
			sub.cancel()
		}
	}
	l.subs = make(map[string][]*subscription)

	return nil
}
