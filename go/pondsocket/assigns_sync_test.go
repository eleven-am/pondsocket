package pondsocket

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"
)

func TestAssignsSync(t *testing.T) {
	t.Run("assigns updates are synchronized across nodes", func(t *testing.T) {
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

		connA := createTestConn("user1", map[string]interface{}{
			"role": "user",
		})
		connB := createTestConn("user1", map[string]interface{}{
			"role": "user",
		})

		err := channelA.addUser(connA)
		if err != nil {
			t.Fatalf("Failed to add user to channel A: %v", err)
		}

		err = channelB.addUser(connB)
		if err != nil {
			t.Fatalf("Failed to add user to channel B: %v", err)
		}

		err = channelA.UpdateAssigns("user1", "status", "online")
		if err != nil {
			t.Fatalf("Failed to update assigns on channel A: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		assignsB := channelB.GetAssigns()
		userAssignsB, exists := assignsB["user1"]
		if !exists {
			t.Fatal("User1 assigns not found on channel B")
		}

		status, statusExists := userAssignsB["status"]
		if !statusExists {
			t.Error("Status key not found in user1 assigns on channel B")
		} else if status != "online" {
			t.Errorf("Expected status 'online', got '%v'", status)
		}

		err = channelB.UpdateAssigns("user1", "location", "home")
		if err != nil {
			t.Fatalf("Failed to update assigns on channel B: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		assignsA := channelA.GetAssigns()
		userAssignsA, exists := assignsA["user1"]
		if !exists {
			t.Fatal("User1 assigns not found on channel A")
		}

		location, locationExists := userAssignsA["location"]
		if !locationExists {
			t.Error("Location key not found in user1 assigns on channel A")
		} else if location != "home" {
			t.Errorf("Expected location 'home', got '%v'", location)
		}

		if userAssignsA["status"] != "online" {
			t.Error("Status should still be 'online' on channel A after location update")
		}

		assignsB = channelB.GetAssigns()
		userAssignsB, exists = assignsB["user1"]
		if !exists {
			t.Fatal("User1 assigns not found on channel B after update")
		}
		if userAssignsB["location"] != "home" {
			t.Error("Location should be 'home' on channel B after sync")
		}
	})

	t.Run("assigns updates are published to PubSub", func(t *testing.T) {
		ctx := context.Background()

		// Track published messages
		var publishedMessages []PubSubMessage
		var messagesMutex sync.Mutex

		origPubSub := NewLocalPubSub(ctx, 100)
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

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               pubsubWrapper,
		}

		channel := newChannel(ctx, channelOpts)
		channel.endpointPath = "/socket"
		channel.subscribeToPubSub()
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{
			"role": "user",
		})

		err := channel.addUser(conn)
		if err != nil {
			t.Fatalf("Failed to add user: %v", err)
		}

		err = channel.UpdateAssigns("user1", "status", "online")
		if err != nil {
			t.Fatalf("Failed to update assigns: %v", err)
		}

		time.Sleep(100 * time.Millisecond)

		found := false
		messagesMutex.Lock()
		messagesCopy := make([]PubSubMessage, len(publishedMessages))
		copy(messagesCopy, publishedMessages)
		messagesMutex.Unlock()

		for _, msg := range messagesCopy {
			if msg.Topic == "pondsocket:socket:test:channel:assigns:update" {
				var event Event
				if err := json.Unmarshal(msg.Data, &event); err == nil {
					if event.Action == "ASSIGNS" && event.Event == "assigns:update" {
						payload, ok := event.Payload.(map[string]interface{})
						if ok && payload["UserID"] == "user1" && payload["Key"] == "status" && payload["Value"] == "online" {
							found = true
							break
						}
					}
				}
			}
		}

		if !found {
			t.Error("Assigns update was not published to PubSub")
		}
	})

	t.Run("remote assigns updates don't create infinite loops", func(t *testing.T) {
		ctx := context.Background()

		// Track published messages to ensure we don't get infinite loops
		var publishCount int
		var countMutex sync.Mutex

		origPubSub := NewLocalPubSub(ctx, 100)
		pubsubWrapper := &pubsubTestWrapper{
			PubSub: origPubSub,
			onPublish: func(topic string, data []byte) {
				countMutex.Lock()
				publishCount++
				countMutex.Unlock()
			},
		}

		channelOpts := options{
			Name:                 "test:channel",
			Middleware:           newMiddleWare[*messageEvent, *Channel](),
			Outgoing:             newMiddleWare[*OutgoingContext, interface{}](),
			InternalQueueTimeout: 1 * time.Second,
			PubSub:               pubsubWrapper,
		}

		channel := newChannel(ctx, channelOpts)
		channel.endpointPath = "/socket"
		channel.subscribeToPubSub()
		defer channel.Close()

		conn := createTestConn("user1", map[string]interface{}{
			"role": "user",
		})

		err := channel.addUser(conn)
		if err != nil {
			t.Fatalf("Failed to add user: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		topic := formatTopic("socket", "test:channel", "assigns:update")
		assignsEvent := Event{
			Action:      assigns,
			ChannelName: "test:channel",
			RequestId:   "remote-123",
			Event:       "assigns:update",
			Payload: map[string]interface{}{
				"UserID": "user1",
				"Key":    "status",
				"Value":  "online",
			},
		}

		data, err := json.Marshal(assignsEvent)
		if err != nil {
			t.Fatalf("Failed to marshal assigns event: %v", err)
		}

		countMutex.Lock()
		initialPublishCount := publishCount
		countMutex.Unlock()

		err = origPubSub.Publish(topic, data)
		if err != nil {
			t.Fatalf("Failed to publish to PubSub: %v", err)
		}

		time.Sleep(200 * time.Millisecond)

		countMutex.Lock()
		finalPublishCount := publishCount
		countMutex.Unlock()

		if finalPublishCount > initialPublishCount {
			t.Errorf("Remote assigns update triggered additional publishes (infinite loop), initial: %d, final: %d", initialPublishCount, finalPublishCount)
		}

		assigns := channel.GetAssigns()
		userAssigns, exists := assigns["user1"]
		if !exists {
			t.Fatal("User1 assigns not found after remote update")
		}

		status, statusExists := userAssigns["status"]
		if !statusExists {
			t.Error("Status key not found in user1 assigns after remote update")
		} else if status != "online" {
			t.Errorf("Expected status 'online', got '%v'", status)
		}
	})
}
