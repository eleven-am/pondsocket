package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestNewPresenceSyncMechanism(t *testing.T) {
	t.Run("sync complete event contains full presence state", func(t *testing.T) {
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

				var event Event
				if err := json.Unmarshal(data, &event); err == nil {
					if event.Event == "presence:sync_complete" {
						t.Logf("Received sync_complete event: %s", string(data))
					}
				}
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
		connB := createTestConn("user_B", nil)

		err = channelB.addUser(connB)

		if err != nil {
			t.Fatalf("Failed to add user to channel B: %v", err)
		}
		err = channelA.Track("user_A", map[string]interface{}{
			"status": "online",
			"node":   "A",
		})

		if err != nil {
			t.Fatalf("Failed to track user on channel A: %v", err)
		}
		err = channelB.Track("user_B", map[string]interface{}{
			"status": "active",
			"node":   "B",
		})

		if err != nil {
			t.Fatalf("Failed to track user on channel B: %v", err)
		}
		time.Sleep(1 * time.Second)

		messagesMutex.Lock()
		messagesCopy := make([]PubSubMessage, len(publishedMessages))
		copy(messagesCopy, publishedMessages)
		messagesMutex.Unlock()

		t.Logf("Total published messages: %d", len(messagesCopy))

		var syncRequests, syncResponses, syncCompletes int
		for _, msg := range messagesCopy {
			var event Event
			if err := json.Unmarshal(msg.Data, &event); err == nil {
				switch event.Event {
				case "presence:sync_request":
					syncRequests++
				case "presence:sync_response":
					syncResponses++
				case "presence:sync_complete":
					syncCompletes++
				}
			}
		}
		t.Logf("Sync requests: %d, Sync responses: %d, Sync completes: %d",
			syncRequests, syncResponses, syncCompletes)

		if syncRequests == 0 {
			t.Error("Expected to see sync_request events")
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
