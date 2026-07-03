package pondsocket

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestNewPresenceSyncMechanism(t *testing.T) {
	t.Run("state request is emitted and presence propagates without tunneled events", func(t *testing.T) {
		ctx := context.Background()

		var publishedMessages []PubSubMessage
		var messagesMutex sync.Mutex
		origPubSub := NewLocalPubSub(ctx, 100)

		sharedPubSub := &pubsubTestWrapper{
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
		channelOptsA := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               sharedPubSub,
		}
		channelA := newChannel(ctx, channelOptsA)
		channelA.endpointPath = "/socket"
		channelA.subscribeToPubSub()
		defer channelA.Close()

		channelOptsB := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               sharedPubSub,
		}
		channelB := newChannel(ctx, channelOptsB)
		channelB.endpointPath = "/socket"
		channelB.subscribeToPubSub()
		defer channelB.Close()

		time.Sleep(200 * time.Millisecond)

		connA := createTestConn("user_A", nil)
		if err := channelA.addUser(connA); err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}
		connB := createTestConn("user_B", nil)
		if err := channelB.addUser(connB); err != nil {
			t.Fatalf("Failed to add user to channel B: %v", err)
		}
		if err := channelA.Track("user_A", map[string]interface{}{
			"status": "online",
			"node":   "A",
		}); err != nil {
			t.Fatalf("Failed to track user on channel A: %v", err)
		}
		if err := channelB.Track("user_B", map[string]interface{}{
			"status": "active",
			"node":   "B",
		}); err != nil {
			t.Fatalf("Failed to track user on channel B: %v", err)
		}
		time.Sleep(500 * time.Millisecond)

		messagesMutex.Lock()
		messagesCopy := make([]PubSubMessage, len(publishedMessages))
		copy(messagesCopy, publishedMessages)
		messagesMutex.Unlock()

		var stateRequests, presenceUpdates, tunneled int
		for _, msg := range messagesCopy {
			var env map[string]interface{}
			if err := json.Unmarshal(msg.Data, &env); err != nil {
				continue
			}
			switch env["type"] {
			case msgStateRequest:
				stateRequests++
			case msgPresenceUpdate:
				presenceUpdates++
				if _, hasEvent := env["event"]; hasEvent {
					t.Error("PRESENCE_UPDATE must not carry an event field on the wire")
				}
			}
			if ev, ok := env["event"].(string); ok {
				switch ev {
				case "presence:sync_request", "presence:sync_response", "presence:sync_complete":
					tunneled++
				}
			}
		}

		if stateRequests == 0 {
			t.Error("Expected to see STATE_REQUEST messages on the wire")
		}
		if presenceUpdates == 0 {
			t.Error("Expected to see PRESENCE_UPDATE messages on the wire")
		}
		if tunneled != 0 {
			t.Errorf("Expected no tunneled presence sync events, found %d", tunneled)
		}

		presenceA := channelA.GetPresence()
		presenceB := channelB.GetPresence()

		if len(presenceA) != 2 {
			t.Errorf("Channel A should have 2 users, got %d", len(presenceA))
		}
		if len(presenceB) != 2 {
			t.Errorf("Channel B should have 2 users, got %d", len(presenceB))
		}
	})
}
