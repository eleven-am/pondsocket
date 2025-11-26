package distributed

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// TestRedisPubSub_BasicFunctionality tests basic publish/subscribe operations.
func TestRedisPubSub_BasicFunctionality(t *testing.T) {
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available:", err)
	}

	client.FlushDB(ctx)

	pubsub, err := NewRedisPubSub(ctx, client)
	if err != nil {
		t.Fatal("Failed to create RedisPubSub:", err)
	}
	defer pubsub.Close()

	t.Run("PublishSubscribe", func(t *testing.T) {
		received := make(chan struct{})
		var receivedTopic string
		var receivedData []byte

		err := pubsub.Subscribe("pondsocket:test:channel:.*", func(topic string, data []byte) {
			receivedTopic = topic
			receivedData = data
			close(received)
		})
		if err != nil {
			t.Fatal("Subscribe failed:", err)
		}

		time.Sleep(100 * time.Millisecond)

		testData := []byte(`{"test": "message"}`)
		err = pubsub.Publish("pondsocket:test:channel:message", testData)
		if err != nil {
			t.Fatal("Publish failed:", err)
		}

		select {
		case <-received:
			if receivedTopic != "pondsocket:test:channel:message" {
				t.Errorf("Expected topic 'pondsocket:test:channel:message', got '%s'", receivedTopic)
			}
			if string(receivedData) != string(testData) {
				t.Errorf("Expected data '%s', got '%s'", testData, receivedData)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for message")
		}
	})

	t.Run("MultipleSubscribers", func(t *testing.T) {
		var wg sync.WaitGroup
		count := 3
		wg.Add(count)

		for i := 0; i < count; i++ {
			err := pubsub.Subscribe("pondsocket:multi:.*", func(topic string, data []byte) {
				wg.Done()
			})
			if err != nil {
				t.Fatal("Subscribe failed:", err)
			}
		}

		time.Sleep(100 * time.Millisecond)

		err = pubsub.Publish("pondsocket:multi:test", []byte("test"))
		if err != nil {
			t.Fatal("Publish failed:", err)
		}

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:

		case <-time.After(2 * time.Second):
			t.Fatal("Timeout waiting for all handlers")
		}
	})

	t.Run("Unsubscribe", func(t *testing.T) {
		received := make(chan struct{})

		err := pubsub.Subscribe("pondsocket:unsub:.*", func(topic string, data []byte) {
			received <- struct{}{}
		})
		if err != nil {
			t.Fatal("Subscribe failed:", err)
		}

		time.Sleep(100 * time.Millisecond)

		err = pubsub.Publish("pondsocket:unsub:test", []byte("test"))
		if err != nil {
			t.Fatal("Publish failed:", err)
		}

		select {
		case <-received:

		case <-time.After(1 * time.Second):
			t.Fatal("Initial message not received")
		}

		err = pubsub.Unsubscribe("pondsocket:unsub:.*")
		if err != nil {
			t.Fatal("Unsubscribe failed:", err)
		}

		time.Sleep(100 * time.Millisecond)

		err = pubsub.Publish("pondsocket:unsub:test", []byte("test"))
		if err != nil {
			t.Fatal("Publish after unsubscribe failed:", err)
		}

		select {
		case <-received:
			t.Fatal("Received message after unsubscribe")
		case <-time.After(500 * time.Millisecond):

		}
	})
}

// TestRedisPubSub_PatternMatching tests pattern matching functionality.
func TestRedisPubSub_PatternMatching(t *testing.T) {
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available:", err)
	}

	client.FlushDB(ctx)

	pubsub, err := NewRedisPubSub(ctx, client)
	if err != nil {
		t.Fatal("Failed to create RedisPubSub:", err)
	}
	defer pubsub.Close()

	tests := []struct {
		name        string
		pattern     string
		topic       string
		shouldMatch bool
	}{
		{"Exact match", "pondsocket:app:channel:event", "pondsocket:app:channel:event", true},
		{"Wildcard match", "pondsocket:app:channel:.*", "pondsocket:app:channel:event", true},
		{"Wildcard no match", "pondsocket:app:channel:.*", "pondsocket:app:other:event", false},
		{"No wildcard no match", "pondsocket:app:channel:event", "pondsocket:app:channel:other", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			received := make(chan struct{})

			err := pubsub.Subscribe(tt.pattern, func(topic string, data []byte) {
				if topic == tt.topic {
					close(received)
				}
			})
			if err != nil {
				t.Fatal("Subscribe failed:", err)
			}

			time.Sleep(100 * time.Millisecond)

			err = pubsub.Publish(tt.topic, []byte("test"))
			if err != nil {
				t.Fatal("Publish failed:", err)
			}

			select {
			case <-received:
				if !tt.shouldMatch {
					t.Error("Received message when should not match")
				}
			case <-time.After(500 * time.Millisecond):
				if tt.shouldMatch {
					t.Error("Did not receive message when should match")
				}
			}

			pubsub.Unsubscribe(tt.pattern)
		})
	}
}

// TestRedisPubSub_Concurrency tests concurrent operations.
func TestRedisPubSub_Concurrency(t *testing.T) {
	ctx := context.Background()
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:6379",
	})

	if err := client.Ping(ctx).Err(); err != nil {
		t.Skip("Redis not available:", err)
	}

	client.FlushDB(ctx)

	pubsub, err := NewRedisPubSub(ctx, client)
	if err != nil {
		t.Fatal("Failed to create RedisPubSub:", err)
	}
	defer pubsub.Close()

	var wg sync.WaitGroup
	messageCount := 100
	subscriberCount := 5

	received := make([]int, subscriberCount)
	var mu sync.Mutex

	for i := 0; i < subscriberCount; i++ {
		idx := i
		err := pubsub.Subscribe("pondsocket:concurrent:.*", func(topic string, data []byte) {
			mu.Lock()
			received[idx]++
			mu.Unlock()
		})
		if err != nil {
			t.Fatal("Subscribe failed:", err)
		}
	}

	time.Sleep(100 * time.Millisecond)

	wg.Add(messageCount)
	for i := 0; i < messageCount; i++ {
		go func(n int) {
			defer wg.Done()
			topic := fmt.Sprintf("pondsocket:concurrent:msg%d", n)
			err := pubsub.Publish(topic, []byte(fmt.Sprintf("message %d", n)))
			if err != nil {
				t.Error("Publish failed:", err)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(500 * time.Millisecond)

	mu.Lock()
	defer mu.Unlock()
	for i, count := range received {
		if count != messageCount {
			t.Errorf("Subscriber %d received %d messages, expected %d", i, count, messageCount)
		}
	}
}
