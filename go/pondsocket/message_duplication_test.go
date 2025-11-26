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

		ctx := context.Background()
		pubsub := NewLocalPubSub(ctx, 100)
		defer pubsub.Close()

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

		channel1.endpointPath = "/socket"
		channel2.endpointPath = "/socket"

		channel1.subscribeToPubSub()
		channel2.subscribeToPubSub()

		conn1 := createTestConn("user1", make(map[string]interface{}))
		conn2 := createTestConn("user2", make(map[string]interface{}))

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

		err = channel1.Broadcast("test_event", map[string]interface{}{
			"message": "hello world",
		})
		if err != nil {
			t.Fatal(err)
		}

		time.Sleep(200 * time.Millisecond)

		mu.Lock()
		user1MessageCount := len(messages1)
		user2MessageCount := len(messages2)
		mu.Unlock()

		if user1MessageCount != 1 {
			t.Errorf("Expected user1 to receive 1 message, got %d", user1MessageCount)
		}
		if user2MessageCount != 1 {
			t.Errorf("Expected user2 to receive 1 message, got %d", user2MessageCount)
		}

		if user1MessageCount > 0 && user2MessageCount > 0 {
			mu.Lock()
			msg1 := messages1[0]
			msg2 := messages2[0]
			mu.Unlock()

			if msg1.Event != msg2.Event || msg1.Event != "test_event" {
				t.Errorf("Messages don't match: %v vs %v", msg1.Event, msg2.Event)
			}
		}

		channel1.Close()
		channel2.Close()
	})
}

func TestNodeIDFiltering(t *testing.T) {
	t.Run("filters messages from same nodeID", func(t *testing.T) {

		ctx := context.Background()
		pubsub := NewLocalPubSub(ctx, 100)
		defer pubsub.Close()

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               pubsub,
		}

		channel := newChannel(ctx, channelOpts)

		channel.endpointPath = "/socket"

		channel.subscribeToPubSub()

		conn := createTestConn("user1", make(map[string]interface{}))

		err := channel.addUser(conn)
		if err != nil {
			t.Fatal(err)
		}

		// Track messages received
		var messages []Event
		var mu sync.Mutex

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

		err = channel.Broadcast("test_event", map[string]interface{}{
			"message": "hello world",
		})
		if err != nil {
			t.Fatal(err)
		}

		time.Sleep(200 * time.Millisecond)

		mu.Lock()
		messageCount := len(messages)
		mu.Unlock()

		if messageCount != 1 {
			t.Errorf("Expected to receive 1 message, got %d. This indicates message duplication was not prevented.", messageCount)
		}

		channel.Close()
	})
}
