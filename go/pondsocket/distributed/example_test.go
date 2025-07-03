package distributed_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/eleven-am/pondsocket/go/pondsocket/distributed"
)

// Example demonstrates how to use RedisPubSub with PondSocket
func Example_redisPubSub() {
	// Create Redis client
	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "", // no password
		DB:       0,  // default DB
	})

	ctx := context.Background()

	// Create Redis PubSub adapter
	pubsub, err := distributed.NewRedisPubSub(ctx, redisClient)
	if err != nil {
		log.Fatal("Failed to create Redis PubSub:", err)
	}
	defer pubsub.Close()

	// Subscribe to channel events
	err = pubsub.Subscribe("pondsocket:api:chat-room:.*", func(topic string, data []byte) {
		fmt.Printf("Received on %s: %s\n", topic, string(data))
	})
	if err != nil {
		log.Fatal("Failed to subscribe:", err)
	}

	// Publish a message
	err = pubsub.Publish("pondsocket:api:chat-room:message", []byte(`{"user": "alice", "text": "Hello!"}`))
	if err != nil {
		log.Fatal("Failed to publish:", err)
	}

	// Wait a bit for message delivery
	time.Sleep(100 * time.Millisecond)
}

// Example_pondSocketIntegration shows how to integrate Redis PubSub with PondSocket
func Example_pondSocketIntegration() {
	// Create Redis client with connection pooling
	redisClient := redis.NewClient(&redis.Options{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 5,
		MaxRetries:   3,
	})

	ctx := context.Background()

	// Create Redis PubSub adapter
	redisPubSub, err := distributed.NewRedisPubSub(ctx, redisClient)
	if err != nil {
		log.Fatal("Failed to create Redis PubSub:", err)
	}

	// Use with PondSocket (pseudo-code as we need the actual PondSocket types)
	/*
		options := pondsocket.DefaultOptions()
		options.PubSub = redisPubSub

		server := pondsocket.NewServer(&pondsocket.ServerOptions{
			Options: options,
		})

		// Now PondSocket will use Redis for distributed messaging
	*/

	// Close when done
	defer redisPubSub.Close()

	fmt.Println("Redis PubSub configured for PondSocket")
}

// Example_multiNodeSetup demonstrates a multi-node setup
func Example_multiNodeSetup() {
	ctx := context.Background()

	// Node 1 setup
	node1Client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	node1PubSub, _ := distributed.NewRedisPubSub(ctx, node1Client)
	defer node1PubSub.Close()

	// Node 2 setup
	node2Client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	node2PubSub, _ := distributed.NewRedisPubSub(ctx, node2Client)
	defer node2PubSub.Close()

	// Node 2 subscribes to presence events
	node2PubSub.Subscribe("pondsocket:api:lobby:presence:.*", func(topic string, data []byte) {
		fmt.Printf("Node 2 received presence update: %s\n", string(data))
	})

	time.Sleep(100 * time.Millisecond) // Allow subscription to register

	// Node 1 publishes a presence join event
	node1PubSub.Publish("pondsocket:api:lobby:presence:join", []byte(`{"userId": "user123", "nodeId": "node1"}`))

	time.Sleep(100 * time.Millisecond) // Allow message delivery
}
