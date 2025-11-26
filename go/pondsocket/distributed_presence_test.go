package pondsocket

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestDistributedPresenceOnJoin(t *testing.T) {
	t.Run("user receives full distributed presence list on join", func(t *testing.T) {
		ctx := context.Background()
		sharedPubSub := NewLocalPubSub(ctx, 100)

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

		connA1 := createTestConn("user_on_A", nil)
		connB1 := createTestConn("user_on_B", nil)

		err := channelA.addUser(connA1)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}

		err = channelB.addUser(connB1)
		if err != nil {
			t.Fatalf("Failed to add user to channel B: %v", err)
		}

		err = channelA.Track("user_on_A", map[string]interface{}{
			"status": "online",
			"node":   "A",
		})
		if err != nil {
			t.Fatalf("Failed to track user on channel A: %v", err)
		}

		err = channelB.Track("user_on_B", map[string]interface{}{
			"status": "active",
			"node":   "B",
		})
		if err != nil {
			t.Fatalf("Failed to track user on channel B: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		clearMessages(connA1)
		clearMessages(connB1)

		connA2 := createTestConn("new_user", nil)
		err = channelA.addUser(connA2)
		if err != nil {
			t.Fatalf("Failed to add new user to channel A: %v", err)
		}

		err = channelA.Track("new_user", map[string]interface{}{
			"status": "joining",
			"node":   "A",
		})
		if err != nil {
			t.Fatalf("Failed to track new user: %v", err)
		}

		time.Sleep(500 * time.Millisecond)

		presence := channelA.GetPresence()

		if len(presence) < 2 {
			t.Errorf("Expected at least 2 users in distributed presence, got %d", len(presence))
		}

		userAPresence, hasUserA := presence["user_on_A"]
		if !hasUserA {
			t.Error("New user should see presence for user_on_A")
		} else {
			userAMap, ok := userAPresence.(map[string]interface{})
			if !ok {
				t.Error("user_on_A presence should be a map")
			} else if userAMap["node"] != "A" {
				t.Error("user_on_A should be from node A")
			}
		}

		userBPresence, hasUserB := presence["user_on_B"]
		if !hasUserB {
			t.Error("New user should see presence for user_on_B")
		} else {
			userBMap, ok := userBPresence.(map[string]interface{})
			if !ok {
				t.Error("user_on_B presence should be a map")
			} else if userBMap["node"] != "B" {
				t.Error("user_on_B should be from node B")
			}
		}
	})

	t.Run("presence sync request triggers presence sharing", func(t *testing.T) {
		ctx := context.Background()
		sharedPubSub := NewLocalPubSub(ctx, 100)

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
		})
		if err != nil {
			t.Fatalf("Failed to track user on channel A: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		topic := formatTopic("socket", "test:channel", "presence:sync_request")
		syncEvent := Event{
			Action:      presence,
			ChannelName: "test:channel",
			RequestId:   "sync-123",
			Event:       "presence:sync_request",
			Payload:     map[string]interface{}{},
		}

		data, err := json.Marshal(syncEvent)
		if err != nil {
			t.Fatalf("Failed to marshal sync event: %v", err)
		}

		err = sharedPubSub.Publish(topic, data)
		if err != nil {
			t.Fatalf("Failed to publish sync request: %v", err)
		}

		time.Sleep(300 * time.Millisecond)

		presenceB := channelB.GetPresence()
		userAPresence, hasUserA := presenceB["user_A"]
		if !hasUserA {
			t.Error("Channel B should have received user_A's presence via sync")
		} else {
			userAMap, ok := userAPresence.(map[string]interface{})
			if !ok {
				t.Error("user_A presence should be a map")
			} else if userAMap["status"] != "online" {
				t.Error("user_A status should be 'online'")
			}
		}
	})
}

// Helper function to clear message queues
func clearMessages(conn *Conn) {
	for {
		select {
		case <-conn.send:

		default:
			return
		}
	}
}
