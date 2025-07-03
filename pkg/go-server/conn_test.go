package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gorilla/websocket"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func mockWebSocketServer(t *testing.T, handler func(*websocket.Conn)) *httptest.Server {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)

		if err != nil {
			t.Fatalf("Failed to upgrade connection: %v", err)
		}
		defer conn.Close()

		handler(conn)
	}))
}

func createClientConn(t *testing.T, serverURL string) *websocket.Conn {
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http")

	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)

	if err != nil {
		t.Fatalf("Failed to dial WebSocket: %v", err)
	}
	return conn
}

func TestNewConn(t *testing.T) {
	t.Run("creates connection successfully", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(100 * time.Millisecond)
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		assigns := map[string]interface{}{"role": "user"}
		conn, err := newConn(ctx, wsConn, assigns, "test-id", opts)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer conn.Close()

		if conn.ID != "test-id" {
			t.Errorf("expected ID test-id, got %s", conn.ID)
		}
		if conn.GetAssign("role") != "user" {
			t.Errorf("expected role to be user, got %v", conn.GetAssign("role"))
		}
	})

	t.Run("sets up read deadline correctly", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(100 * time.Millisecond)
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		conn, err := newConn(ctx, wsConn, nil, "test-id", opts)

		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		defer conn.Close()

		if !conn.IsActive() {
			t.Error("expected connection to be active")
		}
	})
}

func TestConnSendJSON(t *testing.T) {
	t.Run("sends JSON message successfully", func(t *testing.T) {
		receivedChan := make(chan []byte, 1)

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			_, msg, err := serverConn.ReadMessage()

			if err == nil {
				receivedChan <- msg
			}
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

		defer conn.Close()

		testEvent := Event{
			Action:      "test",
			ChannelName: "test-channel",
			RequestId:   "req-123",
			Event:       "test-event",
			Payload:     "test-payload",
		}
		err := conn.sendJSON(testEvent)

		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		select {
		case msg := <-receivedChan:
			var received Event
			if err := json.Unmarshal(msg, &received); err != nil {
				t.Fatalf("failed to unmarshal received message: %v", err)
			}
			if received.RequestId != "req-123" {
				t.Errorf("expected request ID req-123, got %s", received.RequestId)
			}
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for message")
		}
	})

	t.Run("returns error when connection is closing", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(10 * time.Millisecond)
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

		conn.Close()

		err := conn.sendJSON(Event{})

		if err == nil {
			t.Error("expected error when sending to closed connection")
		}
	})
}

func TestConnAssigns(t *testing.T) {
	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		time.Sleep(100 * time.Millisecond)
	})

	defer server.Close()

	wsConn := createClientConn(t, server.URL)

	defer wsConn.Close()

	ctx := context.Background()

	opts := DefaultOptions()

	conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

	defer conn.Close()

	t.Run("setAssign and GetAssign", func(t *testing.T) {
		conn.setAssign("key1", "value1")

		conn.setAssign("key2", 42)

		if conn.GetAssign("key1") != "value1" {
			t.Errorf("expected value1, got %v", conn.GetAssign("key1"))
		}
		if conn.GetAssign("key2") != 42 {
			t.Errorf("expected 42, got %v", conn.GetAssign("key2"))
		}
		if conn.GetAssign("nonexistent") != nil {
			t.Errorf("expected nil for nonexistent key, got %v", conn.GetAssign("nonexistent"))
		}
	})

	t.Run("cloneAssigns", func(t *testing.T) {
		conn.setAssign("key3", "value3")

		cloned := conn.cloneAssigns()

		if cloned["key1"] != "value1" {
			t.Errorf("expected value1 in clone, got %v", cloned["key1"])
		}
		cloned["key1"] = "modified"
		if conn.GetAssign("key1") != "value1" {
			t.Error("modifying clone affected original")
		}
	})
}

func TestConnOnMessage(t *testing.T) {
	messageReceived := make(chan Event, 1)

	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		event := Event{
			Action:      "test",
			ChannelName: "test-channel",
			RequestId:   "req-123",
			Event:       "test-event",
			Payload:     "test-payload",
		}
		data, _ := json.Marshal(event)

		serverConn.WriteMessage(websocket.TextMessage, data)

		time.Sleep(100 * time.Millisecond)
	})

	defer server.Close()

	wsConn := createClientConn(t, server.URL)

	defer wsConn.Close()

	ctx := context.Background()

	opts := DefaultOptions()

	testConn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

	defer testConn.Close()

	testConn.onMessage(func(event Event, c *Conn) error {
		messageReceived <- event
		return nil
	})

	testConn.handleMessages()

	select {
	case event := <-messageReceived:
		if event.RequestId != "req-123" {
			t.Errorf("expected request ID req-123, got %s", event.RequestId)
		}
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for message handler to be called")
	}
}

func TestConnOnClose(t *testing.T) {
	closeCalled := make(chan bool, 1)

	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		time.Sleep(50 * time.Millisecond)
	})

	defer server.Close()

	wsConn := createClientConn(t, server.URL)

	defer wsConn.Close()

	ctx := context.Background()

	opts := DefaultOptions()

	testConn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

	testConn.OnClose(func(c *Conn) error {
		closeCalled <- true
		return nil
	})

	testConn.Close()

	select {
	case <-closeCalled:
	case <-time.After(1 * time.Second):
		t.Error("timeout waiting for close handler to be called")
	}
}

func TestConnConcurrentAccess(t *testing.T) {
	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		time.Sleep(200 * time.Millisecond)
	})

	defer server.Close()

	wsConn := createClientConn(t, server.URL)

	defer wsConn.Close()

	ctx := context.Background()

	opts := DefaultOptions()

	conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

	defer conn.Close()

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := fmt.Sprintf("key%d", n)

			conn.setAssign(key, n)
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			key := fmt.Sprintf("key%d", n)

			time.Sleep(10 * time.Millisecond)

			val := conn.GetAssign(key)

			if val != nil && val != n {
				t.Errorf("expected %d for key %s, got %v", n, key, val)
			}
		}(i)
	}
	for i := 0; i < 10; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			_ = conn.IsActive()
		}()
	}
	wg.Wait()
}

func TestConnReadPump(t *testing.T) {
	t.Run("closes on read error", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			serverConn.Close()
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		opts.PongWait = 100 * time.Millisecond
		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

		time.Sleep(200 * time.Millisecond)

		if conn.IsActive() {
			t.Error("expected connection to be inactive after read error")
		}
	})

	t.Run("handles message timeout", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			for i := 0; i < 100; i++ {
				event := Event{
					Action:      "test",
					ChannelName: "test-channel",
					RequestId:   fmt.Sprintf("req-%d", i),
					Event:       "test-event",
				}
				data, _ := json.Marshal(event)

				serverConn.WriteMessage(websocket.TextMessage, data)

				time.Sleep(1 * time.Millisecond)
			}
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		opts.ReceiveChannelBuffer = 1
		opts.WriteWait = 50 * time.Millisecond
		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

		defer conn.Close()

		time.Sleep(200 * time.Millisecond)

		if conn.IsActive() {
			t.Error("expected connection to close due to receive timeout")
		}
	})
}

func TestConnWritePump(t *testing.T) {
	t.Run("batches multiple messages", func(t *testing.T) {
		receivedMessages := make(chan string, 10)

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			for {
				_, msg, err := serverConn.ReadMessage()

				if err != nil {
					return
				}
				receivedMessages <- string(msg)
			}
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		opts.PingInterval = 1 * time.Hour
		c := &Conn{
			ID:            "test-id",
			conn:          wsConn,
			send:          make(chan []byte, 256),
			receive:       make(chan []byte, 256),
			ctx:           ctx,
			cancel:        func() {},
			closeChan:     make(chan struct{}),
			closeHandlers: newArray[func(*Conn) error](),
			options:       opts,
			isClosing:     false,
		}
		for i := 0; i < 3; i++ {
			event := Event{
				Action:      "test",
				ChannelName: "test-channel",
				RequestId:   fmt.Sprintf("req-%d", i),
				Event:       "test-event",
			}
			data, _ := json.Marshal(event)

			c.send <- data
		}
		go c.writePump()

		defer c.Close()

		select {
		case msg := <-receivedMessages:
			if !strings.Contains(msg, "req-0") || !strings.Contains(msg, "req-1") || !strings.Contains(msg, "req-2") {
				t.Errorf("expected batched message to contain all events, got: %s", msg)
			}
			newlineCount := strings.Count(msg, "\n")

			if newlineCount < 2 {
				t.Errorf("expected at least 2 newlines in batched message, got %d", newlineCount)
			}
		case <-time.After(1 * time.Second):
			t.Error("timeout waiting for batched message")
		}
	})

	t.Run("sends ping messages", func(t *testing.T) {
		pingReceived := make(chan bool, 1)

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			serverConn.SetPingHandler(func(data string) error {
				pingReceived <- true
				return serverConn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
			})

			for {
				_, _, err := serverConn.ReadMessage()

				if err != nil {
					return
				}
			}
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()

		opts.PingInterval = 50 * time.Millisecond
		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)

		defer conn.Close()

		select {
		case <-pingReceived:
		case <-time.After(200 * time.Millisecond):
			t.Error("timeout waiting for ping message")
		}
	})
}
