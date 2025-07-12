package pondsocket

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestMessageDuplicationPrevention(t *testing.T) {
	t.Run("prevents message duplication in distributed setup", func(t *testing.T) {
		// Create a local PubSub for testing
		ctx := context.Background()
		pubsub := NewLocalPubSub(ctx, 100)
		defer pubsub.Close()

		// Create two channels on the same PubSub (simulating distributed nodes)
		channelOpts1 := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               pubsub,
		}

		channelOpts2 := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               pubsub,
		}

		channel1 := newChannel(ctx, channelOpts1)
		channel2 := newChannel(ctx, channelOpts2)

		// Set up the channels' endpoint paths for PubSub
		channel1.endpointPath = "/socket"
		channel2.endpointPath = "/socket"

		// Subscribe to PubSub for both channels
		channel1.subscribeToPubSub()
		channel2.subscribeToPubSub()

		// Create mock connections
		conn1 := createTestConn("user1", make(map[string]interface{}))
		conn2 := createTestConn("user2", make(map[string]interface{}))

		// Add users to channels
		err := channel1.addUser(conn1)
		if err != nil {
			t.Fatal(err)
		}
		err = channel2.addUser(conn2)
		if err != nil {
			t.Fatal(err)
		}

		// Track messages received by each connection
		var messages1 []Event
		var messages2 []Event
		var mu sync.Mutex

		// Monitor channel send channels for messages
		go func() {
			for {
				select {
				case msg := <-conn1.send:
					var event Event
					if err := json.Unmarshal(msg, &event); err == nil {
						mu.Lock()
						messages1 = append(messages1, event)
						mu.Unlock()
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		go func() {
			for {
				select {
				case msg := <-conn2.send:
					var event Event
					if err := json.Unmarshal(msg, &event); err == nil {
						mu.Lock()
						messages2 = append(messages2, event)
						mu.Unlock()
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		// Send a broadcast message from channel1
		err = channel1.Broadcast("test_event", map[string]interface{}{
			"message": "hello world",
		})
		if err != nil {
			t.Fatal(err)
		}

		// Wait for message processing
		time.Sleep(200 * time.Millisecond)

		// Check that user1 received exactly one message (not duplicated)
		mu.Lock()
		user1MessageCount := len(messages1)
		user2MessageCount := len(messages2)
		mu.Unlock()

		// user1 should receive exactly 1 message (from local delivery)
		// user2 should receive exactly 1 message (from PubSub delivery)
		if user1MessageCount != 1 {
			t.Errorf("Expected user1 to receive 1 message, got %d", user1MessageCount)
		}
		if user2MessageCount != 1 {
			t.Errorf("Expected user2 to receive 1 message, got %d", user2MessageCount)
		}

		// Verify that the messages are the same
		if user1MessageCount > 0 && user2MessageCount > 0 {
			mu.Lock()
			msg1 := messages1[0]
			msg2 := messages2[0]
			mu.Unlock()

			if msg1.Event != msg2.Event || msg1.Event != "test_event" {
				t.Errorf("Messages don't match: %v vs %v", msg1.Event, msg2.Event)
			}
		}

		// Clean up
		channel1.Close()
		channel2.Close()
	})
}

func TestNodeIDFiltering(t *testing.T) {
	t.Run("filters messages from same nodeID", func(t *testing.T) {
		// Create a local PubSub for testing
		ctx := context.Background()
		pubsub := NewLocalPubSub(ctx, 100)
		defer pubsub.Close()

		// Create a channel
		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               pubsub,
		}

		channel := newChannel(ctx, channelOpts)

		// Set up the channel's endpoint path for PubSub
		channel.endpointPath = "/socket"

		// Subscribe to PubSub
		channel.subscribeToPubSub()

		// Create mock connection
		conn := createTestConn("user1", make(map[string]interface{}))

		// Add user to channel
		err := channel.addUser(conn)
		if err != nil {
			t.Fatal(err)
		}

		// Track messages received
		var messages []Event
		var mu sync.Mutex

		// Monitor channel send channel for messages
		go func() {
			for {
				select {
				case msg := <-conn.send:
					var event Event
					if err := json.Unmarshal(msg, &event); err == nil {
						mu.Lock()
						messages = append(messages, event)
						mu.Unlock()
					}
				case <-ctx.Done():
					return
				}
			}
		}()

		// Send a broadcast message
		err = channel.Broadcast("test_event", map[string]interface{}{
			"message": "hello world",
		})
		if err != nil {
			t.Fatal(err)
		}

		// Wait for message processing
		time.Sleep(200 * time.Millisecond)

		// Check that user received exactly one message (not duplicated)
		mu.Lock()
		messageCount := len(messages)
		mu.Unlock()

		if messageCount != 1 {
			t.Errorf("Expected to receive 1 message, got %d. This indicates message duplication was not prevented.", messageCount)
		}

		// Clean up
		channel.Close()
	})
}
