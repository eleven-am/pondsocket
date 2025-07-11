package main

import (
	"fmt"
	"log"
	"time"

	"pondsocket"
)

func main() {
	// Create a new PondSocket client
	client, err := pondsocket.NewPondClient("ws://localhost:4000/socket", map[string]interface{}{
		"token": "your-auth-token",
	})
	if err != nil {
		log.Fatal("Failed to create client:", err)
	}

	// Subscribe to connection changes
	unsubConnection := client.OnConnectionChange(func(connected bool) {
		if connected {
			fmt.Println("✅ Connected to PondSocket server")
		} else {
			fmt.Println("❌ Disconnected from PondSocket server")
		}
	})
	defer unsubConnection()

	// Connect to the server
	err = client.Connect()
	if err != nil {
		log.Fatal("Failed to connect:", err)
	}

	// Create a channel
	channel := client.CreateChannel("lobby", map[string]interface{}{
		"user_id": "user123",
		"name":    "John Doe",
	})

	// Subscribe to channel state changes
	unsubState := channel.OnChannelStateChange(func(state pondsocket.ChannelState) {
		fmt.Printf("📡 Channel state changed to: %s\n", state)
	})
	defer unsubState()

	// Subscribe to messages
	unsubMessages := channel.OnMessage(func(event string, payload pondsocket.PondMessage) {
		fmt.Printf("📨 Received message: %s -> %v\n", event, payload)
	})
	defer unsubMessages()

	// Subscribe to user joins
	unsubJoins := channel.OnJoin(func(presence pondsocket.PondPresence) {
		fmt.Printf("👋 User joined: %v\n", presence)
	})
	defer unsubJoins()

	// Subscribe to user leaves
	unsubLeaves := channel.OnLeave(func(presence pondsocket.PondPresence) {
		fmt.Printf("👋 User left: %v\n", presence)
	})
	defer unsubLeaves()

	// Subscribe to presence changes
	unsubPresence := channel.OnUsersChange(func(users []pondsocket.PondPresence) {
		fmt.Printf("👥 Current users (%d): %v\n", len(users), users)
	})
	defer unsubPresence()

	// Join the channel
	fmt.Println("🔌 Joining channel...")
	channel.Join()

	// Wait a bit for the join to complete
	time.Sleep(2 * time.Second)

	// Send some messages
	fmt.Println("📤 Sending messages...")
	channel.SendMessage("chat_message", pondsocket.PondMessage{
		"text":      "Hello from Go client!",
		"timestamp": time.Now().Unix(),
	})

	channel.SendMessage("user_action", pondsocket.PondMessage{
		"action": "wave",
		"target": "everyone",
	})

	// Send a message and wait for response (with timeout)
	fmt.Println("📤 Sending message with response expectation...")
	responseChan, err := channel.SendForResponse("ping", pondsocket.PondMessage{
		"message": "ping",
	}, 5*time.Second)

	if err != nil {
		log.Printf("Failed to send message for response: %v", err)
	} else {
		select {
		case response := <-responseChan:
			fmt.Printf("📨 Received response: %v\n", response)
		case <-time.After(6 * time.Second):
			fmt.Println("⏰ Response timeout")
		}
	}

	// Keep the program running for a while to see events
	fmt.Println("⏳ Listening for events... (press Ctrl+C to exit)")
	time.Sleep(30 * time.Second)

	// Leave the channel
	fmt.Println("👋 Leaving channel...")
	channel.Leave()

	// Disconnect
	fmt.Println("🔌 Disconnecting...")
	err = client.Disconnect()
	if err != nil {
		log.Printf("Error during disconnect: %v", err)
	}

	fmt.Println("✅ Example completed")
}
