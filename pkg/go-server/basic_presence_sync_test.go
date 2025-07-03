package pondsocket

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBasicPresenceSync(t *testing.T) {
	t.Run("presence events are synchronized between nodes", func(t *testing.T) {
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

				t.Logf("Published to topic %s: %s", topic, string(data))
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

		err := channelA.addUser(connA)

		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}
		err = channelA.Track("user_A", map[string]interface{}{
			"status": "online",
			"node":   "A",
		})

		if err != nil {
			t.Fatalf("Failed to track user on channel A: %v", err)
		}
		time.Sleep(500 * time.Millisecond)

		presenceB := channelB.GetPresence()

		t.Logf("Channel B presence: %+v", presenceB)

		userAPresence, hasUserA := presenceB["user_A"]
		if !hasUserA {
			t.Error("Channel B should have received user_A's presence")
		} else {
			userAMap, ok := userAPresence.(map[string]interface{})

			if !ok {
				t.Error("user_A presence should be a map")
			} else {
				t.Logf("user_A presence on channel B: %+v", userAMap)

				if userAMap["status"] != "online" {
					t.Error("user_A status should be 'online'")
				}
				if userAMap["node"] != "A" {
					t.Error("user_A node should be 'A'")
				}
			}
		}

		connB := createTestConn("user_B", nil)
		err = channelB.addUser(connB)

		if err != nil {
			t.Fatalf("Failed to add user to channel B: %v", err)
		}
		err = channelB.Track("user_B", map[string]interface{}{
			"status": "active",
			"node":   "B",
		})

		if err != nil {
			t.Fatalf("Failed to track user on channel B: %v", err)
		}
		time.Sleep(500 * time.Millisecond)

		presenceA := channelA.GetPresence()

		t.Logf("Channel A presence: %+v", presenceA)

		userBPresence, hasUserB := presenceA["user_B"]
		if !hasUserB {
			t.Error("Channel A should have received user_B's presence")
		} else {
			userBMap, ok := userBPresence.(map[string]interface{})

			if !ok {
				t.Error("user_B presence should be a map")
			} else {
				t.Logf("user_B presence on channel A: %+v", userBMap)

				if userBMap["status"] != "active" {
					t.Error("user_B status should be 'active'")
				}
				if userBMap["node"] != "B" {
					t.Error("user_B node should be 'B'")
				}
			}
		}

		presenceB = channelB.GetPresence()
		t.Logf("FINAL Channel B presence: %+v", presenceB)

		if len(presenceA) != 2 {
			t.Errorf("Channel A should have 2 users, got %d", len(presenceA))
		}
		if len(presenceB) != 2 {
			t.Errorf("Channel B should have 2 users, got %d", len(presenceB))
		}
	})
}
