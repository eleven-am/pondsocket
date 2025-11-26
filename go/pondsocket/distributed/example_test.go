package distributed_test

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/eleven-am/pondsocket/go/pondsocket/distributed"
	"github.com/redis/go-redis/v9"
)

// Example demonstrates how to use RedisPubSub with PondSocket
func Example_redisPubSub() {

	redisClient := redis.NewClient(&redis.Options{
		Addr:     "localhost:6379",
		Password: "",
		DB:       0,
	})

	ctx := context.Background()

	pubsub, err := distributed.NewRedisPubSub(ctx, redisClient)
	if err != nil {
		log.Fatal("Failed to create Redis PubSub:", err)
	}
	defer pubsub.Close()

	err = pubsub.Subscribe("pondsocket:api:chat-room:.*", func(topic string, data []byte) {
		fmt.Printf("Received on %s: %s\n", topic, string(data))
	})
	if err != nil {
		log.Fatal("Failed to subscribe:", err)
	}

	err = pubsub.Publish("pondsocket:api:chat-room:message", []byte(`{"user": "alice", "text": "Hello!"}`))
	if err != nil {
		log.Fatal("Failed to publish:", err)
	}

	time.Sleep(100 * time.Millisecond)
}

// Example_pondSocketIntegration shows how to integrate Redis PubSub with PondSocket
func Example_pondSocketIntegration() {

	redisClient := redis.NewClient(&redis.Options{
		Addr:         "localhost:6379",
		Password:     "",
		DB:           0,
		PoolSize:     10,
		MinIdleConns: 5,
		MaxRetries:   3,
	})

	ctx := context.Background()

	redisPubSub, err := distributed.NewRedisPubSub(ctx, redisClient)
	if err != nil {
		log.Fatal("Failed to create Redis PubSub:", err)
	}

	defer redisPubSub.Close()

	fmt.Println("Redis PubSub configured for PondSocket")
}

// Example_multiNodeSetup demonstrates a multi-node setup
func Example_multiNodeSetup() {
	ctx := context.Background()

	node1Client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	node1PubSub, _ := distributed.NewRedisPubSub(ctx, node1Client)
	defer node1PubSub.Close()

	node2Client := redis.NewClient(&redis.Options{Addr: "localhost:6379"})
	node2PubSub, _ := distributed.NewRedisPubSub(ctx, node2Client)
	defer node2PubSub.Close()

	node2PubSub.Subscribe("pondsocket:api:lobby:presence:.*", func(topic string, data []byte) {
		fmt.Printf("Node 2 received presence update: %s\n", string(data))
	})

	time.Sleep(100 * time.Millisecond)

	node1PubSub.Publish("pondsocket:api:lobby:presence:join", []byte(`{"userId": "user123", "nodeId": "node1"}`))

	time.Sleep(100 * time.Millisecond)
}
