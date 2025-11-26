package pondsocket

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestPubSubIntegration(t *testing.T) {
	t.Run("channel broadcasts messages via PubSub", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		opts.PubSub = NewLocalPubSub(ctx, 100)

		// Track published messages
		var publishedMessages []PubSubMessage
		var messagesMutex sync.Mutex

		origPubSub := opts.PubSub
		pubsubWrapper := &pubsubTestWrapper{
			PubSub: origPubSub,
			onPublish: func(topic string, data []byte) {
				messagesMutex.Lock()
				publishedMessages = append(publishedMessages, PubSubMessage{
					Topic: topic,
					Data:  data,
				})
				messagesMutex.Unlock()
			},
		}
		opts.PubSub = pubsubWrapper

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               opts.PubSub,
		}

		channel := newChannel(ctx, channelOpts)
		channel.endpointPath = "/socket"
		channel.subscribeToPubSub()
		defer channel.Close()

		conn1 := createTestConn("user1", nil)
		conn2 := createTestConn("user2", nil)

		err := channel.addUser(conn1)
		if err != nil {
			t.Fatalf("Failed to add user1: %v", err)
		}
		err = channel.addUser(conn2)
		if err != nil {
			t.Fatalf("Failed to add user2: %v", err)
		}

		err = channel.Broadcast("test:event", map[string]interface{}{
			"message": "Hello from test",
		})
		if err != nil {
			t.Fatalf("Failed to broadcast: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		messagesMutex.Lock()
		messagesCopy := make([]PubSubMessage, len(publishedMessages))
		copy(messagesCopy, publishedMessages)
		messagesMutex.Unlock()

		if len(messagesCopy) == 0 {
			t.Error("Expected message to be published to PubSub")
		} else {
			expectedTopic := "pondsocket:socket:test:channel:test:event"
			if messagesCopy[0].Topic != expectedTopic {
				t.Errorf("Expected topic %s, got %s", expectedTopic, messagesCopy[0].Topic)
			}
		}

		select {
		case msg := <-conn1.send:
			var event Event
			if err := json.Unmarshal(msg, &event); err != nil {
				t.Errorf("Failed to unmarshal conn1 message: %v", err)
			} else if event.Event != "test:event" {
				t.Errorf("Expected event test:event, got %s", event.Event)
			}
		default:
			t.Error("Expected conn1 to receive message")
		}

		select {
		case msg := <-conn2.send:
			var event Event
			if err := json.Unmarshal(msg, &event); err != nil {
				t.Errorf("Failed to unmarshal conn2 message: %v", err)
			} else if event.Event != "test:event" {
				t.Errorf("Expected event test:event, got %s", event.Event)
			}
		default:
			t.Error("Expected conn2 to receive message")
		}
	})

	t.Run("channel receives messages from PubSub", func(t *testing.T) {
		ctx := context.Background()
		opts := DefaultOptions()
		opts.PubSub = NewLocalPubSub(ctx, 100)

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               opts.PubSub,
		}

		channel := newChannel(ctx, channelOpts)
		channel.endpointPath = "/socket"
		channel.subscribeToPubSub()
		defer channel.Close()

		time.Sleep(200 * time.Millisecond)

		conn := createTestConn("user1", nil)
		err := channel.addUser(conn)
		if err != nil {
			t.Fatalf("Failed to add user: %v", err)
		}

		topic := formatTopic("socket", "test:channel", "remote:event")
		event := Event{
			Action:      broadcast,
			ChannelName: "test:channel",
			RequestId:   "remote-123",
			Event:       "remote:event",
			Payload: map[string]interface{}{
				"message": "Hello from remote node",
			},
		}

		data, err := json.Marshal(event)
		if err != nil {
			t.Fatalf("Failed to marshal event: %v", err)
		}

		err = opts.PubSub.Publish(topic, data)
		if err != nil {
			t.Fatalf("Failed to publish to PubSub: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		select {
		case msg := <-conn.send:
			var event Event
			if err := json.Unmarshal(msg, &event); err != nil {
				t.Errorf("Failed to unmarshal received message: %v", err)
			} else if event.Event != "remote:event" {
				t.Errorf("Expected event 'remote:event', got '%s'", event.Event)
			}
		default:
			t.Error("Expected connection to receive message from PubSub")
		}
	})

	t.Run("channel unsubscribes on close", func(t *testing.T) {
		ctx := context.Background()

		trackingPubSub := &subscriptionTracker{
			PubSub:        NewLocalPubSub(ctx, 100),
			subscriptions: make(map[string]bool),
		}

		opts := DefaultOptions()
		opts.PubSub = trackingPubSub

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               opts.PubSub,
		}

		channel := newChannel(ctx, channelOpts)
		channel.endpointPath = "/socket"
		channel.subscribeToPubSub()

		time.Sleep(200 * time.Millisecond)

		pattern := "pondsocket:socket:test:channel:.*"
		trackingPubSub.mu.Lock()
		subscribed := trackingPubSub.subscriptions[pattern]
		trackingPubSub.mu.Unlock()
		if !subscribed {
			t.Errorf("Expected channel to be subscribed to pattern %s", pattern)
		}

		err := channel.Close()
		if err != nil {
			t.Fatalf("Failed to close channel: %v", err)
		}

		trackingPubSub.mu.Lock()
		stillSubscribed := trackingPubSub.subscriptions[pattern]
		trackingPubSub.mu.Unlock()
		if stillSubscribed {
			t.Error("Expected channel to be unsubscribed after close")
		}
	})
}

type pubsubTestWrapper struct {
	PubSub
	onPublish func(topic string, data []byte)
}

func (w *pubsubTestWrapper) Publish(topic string, data []byte) error {
	if w.onPublish != nil {
		w.onPublish(topic, data)
	}
	return w.PubSub.Publish(topic, data)
}

type subscriptionTracker struct {
	PubSub
	subscriptions map[string]bool
	mu            sync.Mutex
}

func (t *subscriptionTracker) Subscribe(pattern string, handler func(topic string, data []byte)) error {
	t.mu.Lock()
	t.subscriptions[pattern] = true
	t.mu.Unlock()
	return t.PubSub.Subscribe(pattern, handler)
}

func (t *subscriptionTracker) Unsubscribe(pattern string) error {
	t.mu.Lock()
	delete(t.subscriptions, pattern)
	t.mu.Unlock()
	return t.PubSub.Unsubscribe(pattern)
}
