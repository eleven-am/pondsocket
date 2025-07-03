package main

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

		// Create two channels on different "nodes" sharing the same PubSub
		// Node 1
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

		// Node 2
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

		// Wait for subscriptions to be set up
		time.Sleep(200 * time.Millisecond)

		// Add existing users with presence on both nodes
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

		// Track presence for users on both nodes
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

		// Give time for presence sync
		time.Sleep(300 * time.Millisecond)

		// Clear message queues before the test
		clearMessages(connA1)
		clearMessages(connB1)

		// Now add a new user to channel A - they should receive distributed presence
		connA2 := createTestConn("new_user", nil)
		err = channelA.addUser(connA2)
		if err != nil {
			t.Fatalf("Failed to add new user to channel A: %v", err)
		}

		// Track the new user to trigger presence join event
		err = channelA.Track("new_user", map[string]interface{}{
			"status": "joining",
			"node":   "A",
		})
		if err != nil {
			t.Fatalf("Failed to track new user: %v", err)
		}

		// Give time for sync request and responses
		time.Sleep(500 * time.Millisecond)

		// Check that the new user can see presence from both nodes
		presence := channelA.GetPresence()

		// Should have presence for all users
		if len(presence) < 2 {
			t.Errorf("Expected at least 2 users in distributed presence, got %d", len(presence))
		}

		// Check specific users
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

		// Create two channels
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

		// Wait for subscriptions to be set up
		time.Sleep(200 * time.Millisecond)

		// Add and track a user on channel A
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

		// Give time for initial presence setup
		time.Sleep(200 * time.Millisecond)

		// Manually trigger a presence sync request from channel B
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

		// Publish sync request
		err = sharedPubSub.Publish(topic, data)
		if err != nil {
			t.Fatalf("Failed to publish sync request: %v", err)
		}

		// Give time for sync response
		time.Sleep(300 * time.Millisecond)

		// Check that channel B now has user_A's presence
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
			// Drain message
		default:
			return
		}
	}
}
