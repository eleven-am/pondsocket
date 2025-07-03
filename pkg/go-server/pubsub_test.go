package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestLocalPubSub(t *testing.T) {
	ctx := context.Background()
	pubsub := NewLocalPubSub(ctx, 10)
	defer pubsub.Close()

	t.Run("publish and subscribe", func(t *testing.T) {
		received := make(chan PubSubMessage, 1)

		err := pubsub.Subscribe("test.topic", func(topic string, data []byte) {
			received <- PubSubMessage{Topic: topic, Data: data}
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		testData := []byte("hello world")
		err = pubsub.Publish("test.topic", testData)
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		select {
		case msg := <-received:
			if msg.Topic != "test.topic" {
				t.Errorf("Expected topic 'test.topic', got '%s'", msg.Topic)
			}
			if string(msg.Data) != "hello world" {
				t.Errorf("Expected data 'hello world', got '%s'", string(msg.Data))
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("Did not receive message within timeout")
		}
	})

	t.Run("multiple subscribers", func(t *testing.T) {
		var wg sync.WaitGroup
		received := make([]string, 0, 3)
		var mu sync.Mutex

		for i := 0; i < 3; i++ {
			wg.Add(1)
			err := pubsub.Subscribe("multi.topic", func(topic string, data []byte) {
				mu.Lock()
				received = append(received, string(data))
				mu.Unlock()
				wg.Done()
			})
			if err != nil {
				t.Fatalf("Subscribe %d failed: %v", i, err)
			}
		}

		time.Sleep(10 * time.Millisecond)

		err := pubsub.Publish("multi.topic", []byte("broadcast"))
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		wg.Wait()

		mu.Lock()
		if len(received) != 3 {
			t.Errorf("Expected 3 messages, got %d", len(received))
		}
		mu.Unlock()
	})

	t.Run("wildcard patterns", func(t *testing.T) {
		received := make(chan string, 2)

		err := pubsub.Subscribe("room.*", func(topic string, data []byte) {
			received <- topic
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// These should match
		pubsub.Publish("room.123", []byte("msg1"))
		pubsub.Publish("room.456", []byte("msg2"))

		// This should not match
		pubsub.Publish("user.123", []byte("msg3"))

		topics := make([]string, 0, 2)
		for i := 0; i < 2; i++ {
			select {
			case topic := <-received:
				topics = append(topics, topic)
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("Timeout waiting for message %d", i+1)
			}
		}

		// Should not receive third message
		select {
		case topic := <-received:
			t.Errorf("Unexpected message received for topic: %s", topic)
		case <-time.After(50 * time.Millisecond):
			// Expected timeout
		}

		if len(topics) != 2 {
			t.Errorf("Expected 2 messages, got %d", len(topics))
		}
	})

	t.Run("unsubscribe", func(t *testing.T) {
		received := make(chan bool, 1)

		err := pubsub.Subscribe("unsub.topic", func(topic string, data []byte) {
			received <- true
		})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// First publish should work
		pubsub.Publish("unsub.topic", []byte("msg1"))

		select {
		case <-received:
			// Expected
		case <-time.After(100 * time.Millisecond):
			t.Error("Did not receive first message")
		}

		// Unsubscribe
		err = pubsub.Unsubscribe("unsub.topic")
		if err != nil {
			t.Fatalf("Unsubscribe failed: %v", err)
		}

		// Second publish should not be received
		pubsub.Publish("unsub.topic", []byte("msg2"))

		select {
		case <-received:
			t.Error("Received message after unsubscribe")
		case <-time.After(50 * time.Millisecond):
			// Expected timeout
		}
	})

	t.Run("close behavior", func(t *testing.T) {
		ps := NewLocalPubSub(ctx, 10)

		err := ps.Subscribe("close.topic", func(topic string, data []byte) {})
		if err != nil {
			t.Fatalf("Subscribe failed: %v", err)
		}

		// Close the pubsub
		err = ps.Close()
		if err != nil {
			t.Fatalf("Close failed: %v", err)
		}

		// Operations after close should fail
		err = ps.Publish("close.topic", []byte("msg"))
		if !isPubSubClosed(err) {
			t.Error("Expected closed error on Publish")
		}

		err = ps.Subscribe("new.topic", func(topic string, data []byte) {})
		if !isPubSubClosed(err) {
			t.Error("Expected closed error on Subscribe")
		}

		err = ps.Unsubscribe("close.topic")
		if !isPubSubClosed(err) {
			t.Error("Expected closed error on Unsubscribe")
		}

		// Close again should be safe
		err = ps.Close()
		if err != nil {
			t.Errorf("Second close returned error: %v", err)
		}
	})
}

func TestTopicFormatting(t *testing.T) {
	tests := []struct {
		name     string
		fn       func() string
		expected string
	}{
		{
			name:     "format message topic",
			fn:       func() string { return formatMessageTopic("socket", "room:123") },
			expected: "pondsocket:socket:room:123:message",
		},
		{
			name:     "format presence topic",
			fn:       func() string { return formatPresenceTopic("admin", "room:456") },
			expected: "pondsocket:admin:room:456:presence",
		},
		{
			name:     "format system topic",
			fn:       func() string { return formatSystemTopic("node-join") },
			expected: "pondsocket:system:node-join",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.fn()
			if result != tt.expected {
				t.Errorf("Expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestTopicMatching(t *testing.T) {
	tests := []struct {
		pattern string
		topic   string
		match   bool
	}{
		// Exact matches
		{"room:123", "room:123", true},
		{"room:123", "room:456", false},

		// Wildcard matches
		{"room.*", "room.123", true},
		{"room.*", "room.456", true},
		{"room.*", "user.123", false},
		{"pondsocket:socket:room.*", "pondsocket:socket:room:123", true},
		{"pondsocket:socket:room.*", "pondsocket:admin:room:123", false},

		// No wildcards in the middle
		{"room.*.test", "room.123.test", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.topic, func(t *testing.T) {
			result := matchTopic(tt.pattern, tt.topic)
			if result != tt.match {
				t.Errorf("matchTopic(%q, %q) = %v, want %v", tt.pattern, tt.topic, result, tt.match)
			}
		})
	}
}
