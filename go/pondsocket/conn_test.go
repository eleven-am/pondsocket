package pondsocket

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
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
		err := conn.SendJSON(testEvent)

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

		err := conn.SendJSON(Event{})

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
		conn.SetAssign("key1", "value1")

		conn.SetAssign("key2", 42)

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
		conn.SetAssign("key3", "value3")

		cloned := conn.CloneAssigns()

		if cloned["key1"] != "value1" {
			t.Errorf("expected value1 in clone, got %v", cloned["key1"])
		}
		cloned["key1"] = "modified"
		if conn.GetAssign("key1") != "value1" {
			t.Error("modifying clone affected original")
		}
	})

	t.Run("getAssigns returns clone not reference", func(t *testing.T) {
		conn.SetAssign("cloneTest", "original")

		assigns := conn.GetAssigns()

		assigns["cloneTest"] = "modified"
		assigns["newKey"] = "newValue"

		if conn.GetAssign("cloneTest") != "original" {
			t.Error("modifying returned map affected original assigns")
		}

		if conn.GetAssign("newKey") != nil {
			t.Error("adding to returned map affected original assigns")
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

	testConn.OnMessage(func(event Event, c Transport) error {
		messageReceived <- event
		return nil
	})

	testConn.HandleMessages()

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

	testConn.OnClose(func(c Transport) error {
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

			conn.SetAssign(key, n)
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
			closeHandlers: newArray[func(Transport) error](),
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

func TestConnSendTimeoutOption(t *testing.T) {
	t.Run("uses configured SendTimeout", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(100 * time.Millisecond)
		})

		defer server.Close()

		wsConn := createClientConn(t, server.URL)

		defer wsConn.Close()

		ctx := context.Background()

		opts := DefaultOptions()
		opts.SendTimeout = 200 * time.Millisecond

		conn, err := newConn(ctx, wsConn, nil, "test-id", opts)
		if err != nil {
			t.Fatalf("failed to create connection: %v", err)
		}
		defer conn.Close()

		if conn.options.SendTimeout != 200*time.Millisecond {
			t.Errorf("expected SendTimeout 200ms, got %v", conn.options.SendTimeout)
		}
	})

	t.Run("default SendTimeout is 5 seconds", func(t *testing.T) {
		opts := DefaultOptions()
		if opts.SendTimeout != 5*time.Second {
			t.Errorf("expected default SendTimeout 5s, got %v", opts.SendTimeout)
		}
	})
}

func TestConnConcurrentHandleMessages(t *testing.T) {
	t.Run("handles multiple messages concurrently", func(t *testing.T) {
		messagesReceived := make(chan string, 20)
		var handlerWg sync.WaitGroup

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			for i := 0; i < 10; i++ {
				event := Event{
					Action:      broadcast,
					ChannelName: "test-channel",
					RequestId:   fmt.Sprintf("req-%d", i),
					Event:       "test-event",
					Payload:     fmt.Sprintf("payload-%d", i),
				}
				data, _ := json.Marshal(event)
				serverConn.WriteMessage(websocket.TextMessage, data)
			}
			time.Sleep(500 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		opts.MaxConcurrentHandlers = 5

		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
		defer conn.Close()

		handlerWg.Add(10)
		conn.OnMessage(func(event Event, c Transport) error {
			defer handlerWg.Done()
			time.Sleep(50 * time.Millisecond)
			messagesReceived <- event.RequestId
			return nil
		})

		conn.HandleMessages()

		done := make(chan struct{})
		go func() {
			handlerWg.Wait()
			close(done)
		}()

		select {
		case <-done:
			if len(messagesReceived) != 10 {
				t.Errorf("expected 10 messages, got %d", len(messagesReceived))
			}
		case <-time.After(3 * time.Second):
			t.Error("timeout waiting for concurrent message handling")
		}
	})

	t.Run("respects MaxConcurrentHandlers limit", func(t *testing.T) {
		var concurrentCount int32
		var maxConcurrent int32
		var mu sync.Mutex

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			for i := 0; i < 20; i++ {
				event := Event{
					Action:      broadcast,
					ChannelName: "test-channel",
					RequestId:   fmt.Sprintf("req-%d", i),
					Event:       "test-event",
				}
				data, _ := json.Marshal(event)
				serverConn.WriteMessage(websocket.TextMessage, data)
			}
			time.Sleep(1 * time.Second)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		opts.MaxConcurrentHandlers = 3

		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
		defer conn.Close()

		var wg sync.WaitGroup
		wg.Add(20)

		conn.OnMessage(func(event Event, c Transport) error {
			defer wg.Done()
			mu.Lock()
			concurrentCount++
			if concurrentCount > maxConcurrent {
				maxConcurrent = concurrentCount
			}
			mu.Unlock()

			time.Sleep(50 * time.Millisecond)

			mu.Lock()
			concurrentCount--
			mu.Unlock()
			return nil
		})

		conn.HandleMessages()

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			if maxConcurrent > 3 {
				t.Errorf("exceeded MaxConcurrentHandlers: max was %d, expected <= 3", maxConcurrent)
			}
		case <-time.After(5 * time.Second):
			t.Error("timeout waiting for messages")
		}
	})

	t.Run("handles handler errors gracefully", func(t *testing.T) {
		errorCount := 0
		var mu sync.Mutex

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			for i := 0; i < 5; i++ {
				event := Event{
					Action:      broadcast,
					ChannelName: "test-channel",
					RequestId:   fmt.Sprintf("req-%d", i),
					Event:       "test-event",
				}
				data, _ := json.Marshal(event)
				serverConn.WriteMessage(websocket.TextMessage, data)
			}
			time.Sleep(300 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
		defer conn.Close()

		var wg sync.WaitGroup
		wg.Add(5)

		conn.OnMessage(func(event Event, c Transport) error {
			defer wg.Done()
			mu.Lock()
			errorCount++
			mu.Unlock()
			return fmt.Errorf("test error for %s", event.RequestId)
		})

		conn.HandleMessages()

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			if errorCount != 5 {
				t.Errorf("expected 5 errors, got %d", errorCount)
			}
		case <-time.After(2 * time.Second):
			t.Error("timeout")
		}
	})

	t.Run("default MaxConcurrentHandlers when not set", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(50 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		opts.MaxConcurrentHandlers = 0

		conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
		defer conn.Close()

		if cap(conn.handlerSem) != 10 {
			t.Errorf("expected default semaphore capacity 10, got %d", cap(conn.handlerSem))
		}
	})
}

func TestConnType(t *testing.T) {
	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		time.Sleep(50 * time.Millisecond)
	})
	defer server.Close()

	wsConn := createClientConn(t, server.URL)
	defer wsConn.Close()

	ctx := context.Background()
	opts := DefaultOptions()
	conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
	defer conn.Close()

	if conn.Type() != TransportWebSocket {
		t.Errorf("expected TransportWebSocket, got %v", conn.Type())
	}
}

func TestConnPushMessage(t *testing.T) {
	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		time.Sleep(50 * time.Millisecond)
	})
	defer server.Close()

	wsConn := createClientConn(t, server.URL)
	defer wsConn.Close()

	ctx := context.Background()
	opts := DefaultOptions()
	conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
	defer conn.Close()

	err := conn.PushMessage([]byte("test"))
	if err == nil {
		t.Error("expected error for PushMessage on WebSocket transport")
	}
}

func TestConnGetID(t *testing.T) {
	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		time.Sleep(50 * time.Millisecond)
	})
	defer server.Close()

	wsConn := createClientConn(t, server.URL)
	defer wsConn.Close()

	ctx := context.Background()
	opts := DefaultOptions()
	conn, _ := newConn(ctx, wsConn, nil, "my-unique-id", opts)
	defer conn.Close()

	if conn.GetID() != "my-unique-id" {
		t.Errorf("expected my-unique-id, got %s", conn.GetID())
	}
}

func TestConnHandleMessagesWithInvalidJSON(t *testing.T) {
	errorSent := make(chan bool, 1)

	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		serverConn.WriteMessage(websocket.TextMessage, []byte("invalid json"))
		for {
			_, _, err := serverConn.ReadMessage()
			if err != nil {
				return
			}
			errorSent <- true
		}
	})
	defer server.Close()

	wsConn := createClientConn(t, server.URL)
	defer wsConn.Close()

	ctx := context.Background()
	opts := DefaultOptions()
	conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
	defer conn.Close()

	conn.OnMessage(func(event Event, c Transport) error {
		return nil
	})
	conn.HandleMessages()

	select {
	case <-errorSent:
	case <-time.After(500 * time.Millisecond):
	}
}

func TestConnHandleMessagesWithInvalidEvent(t *testing.T) {
	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		event := Event{
			Action:      "",
			ChannelName: "",
			RequestId:   "",
			Event:       "",
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
	conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
	defer conn.Close()

	handlerCalled := false
	conn.OnMessage(func(event Event, c Transport) error {
		handlerCalled = true
		return nil
	})
	conn.HandleMessages()

	time.Sleep(150 * time.Millisecond)
	if handlerCalled {
		t.Error("handler should not be called for invalid event")
	}
}

func TestConnHandleMessagesNoHandler(t *testing.T) {
	server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
		event := Event{
			Action:      broadcast,
			ChannelName: "test",
			RequestId:   "req-1",
			Event:       "test",
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
	conn, _ := newConn(ctx, wsConn, nil, "test-id", opts)
	defer conn.Close()

	conn.HandleMessages()
	time.Sleep(150 * time.Millisecond)
}

type connTestMetricsCollector struct {
	errorChan chan string
}

func (m *connTestMetricsCollector) ConnectionOpened(connID string, endpoint string)        {}
func (m *connTestMetricsCollector) ConnectionClosed(connID string, duration time.Duration) {}
func (m *connTestMetricsCollector) ConnectionError(connID string, err error)               {}
func (m *connTestMetricsCollector) MessageReceived(connID string, channel string, event string, size int) {
}
func (m *connTestMetricsCollector) MessageSent(connID string, channel string, event string, size int) {
}
func (m *connTestMetricsCollector) MessageBroadcast(channel string, event string, recipientCount int) {
}
func (m *connTestMetricsCollector) ChannelJoined(userID string, channel string)            {}
func (m *connTestMetricsCollector) ChannelLeft(userID string, channel string)              {}
func (m *connTestMetricsCollector) ChannelCreated(channel string)                          {}
func (m *connTestMetricsCollector) ChannelDestroyed(channel string)                        {}
func (m *connTestMetricsCollector) HandlerDuration(handler string, duration time.Duration) {}
func (m *connTestMetricsCollector) QueueDepth(queue string, depth int)                     {}
func (m *connTestMetricsCollector) Error(component string, err error) {
	if m.errorChan != nil {
		select {
		case m.errorChan <- component:
		default:
		}
	}
}

func TestConnSendJSONTimeout(t *testing.T) {
	t.Run("returns timeout error when send channel is full", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(500 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		opts.SendTimeout = 10 * time.Millisecond

		c := &Conn{
			ID:            "test-timeout",
			conn:          wsConn,
			send:          make(chan []byte, 1),
			receive:       make(chan []byte, 256),
			ctx:           ctx,
			cancel:        func() {},
			closeChan:     make(chan struct{}),
			readDone:      nil,
			closeHandlers: newArray[func(Transport) error](),
			options:       opts,
			isClosing:     false,
		}
		defer c.Close()

		c.send <- []byte(`{"first": "message"}`)

		err := c.SendJSON(Event{RequestId: "second"})
		if err == nil {
			t.Error("expected timeout error when send channel is full")
		}
	})

	t.Run("returns error for non-serializable type", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		conn, _ := newConn(ctx, wsConn, nil, "test-marshal", opts)
		defer conn.Close()

		err := conn.SendJSON(make(chan int))
		if err == nil {
			t.Error("expected error for non-serializable type")
		}
	})

	t.Run("returns error when context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		opts := DefaultOptions()
		opts.SendTimeout = 1 * time.Second

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(500 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		c := &Conn{
			ID:            "test-ctx-cancel",
			conn:          wsConn,
			send:          make(chan []byte, 1),
			receive:       make(chan []byte, 256),
			ctx:           ctx,
			cancel:        cancel,
			closeChan:     make(chan struct{}),
			readDone:      nil,
			closeHandlers: newArray[func(Transport) error](),
			options:       opts,
			isClosing:     false,
		}
		defer c.Close()

		c.send <- []byte(`{"first": "message"}`)

		go func() {
			time.Sleep(20 * time.Millisecond)
			cancel()
		}()

		err := c.SendJSON(Event{RequestId: "second"})
		if err == nil {
			t.Error("expected error when context cancelled")
		}
	})
}

func TestConnReportError(t *testing.T) {
	t.Run("does nothing when metrics is nil", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(50 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		conn, _ := newConn(ctx, wsConn, nil, "test-report-nil", opts)
		defer conn.Close()

		conn.reportError("test", internal("test", "error"))
	})

	t.Run("does nothing when error is nil", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(50 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		conn, _ := newConn(ctx, wsConn, nil, "test-report-nil-err", opts)
		defer conn.Close()

		conn.reportError("test", nil)
	})

	t.Run("calls metrics Error when configured", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		errorReceived := make(chan string, 1)
		opts.Hooks = &Hooks{
			Metrics: &connTestMetricsCollector{errorChan: errorReceived},
		}

		conn, _ := newConn(ctx, wsConn, nil, "test-report-metrics", opts)
		defer conn.Close()

		conn.reportError("test-component", internal("test", "error message"))

		select {
		case component := <-errorReceived:
			if component != "test-component" {
				t.Errorf("expected component 'test-component', got %s", component)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("expected metrics Error to be called")
		}
	})
}

func TestConnGetAssignNilAssigns(t *testing.T) {
	t.Run("returns nil when assigns is nil", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(50 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()

		conn, _ := newConn(ctx, wsConn, nil, "test-nil-assigns", opts)
		defer conn.Close()

		conn.mutex.Lock()
		conn.assigns = nil
		conn.mutex.Unlock()

		val := conn.GetAssign("key")
		if val != nil {
			t.Error("expected nil for nil assigns map")
		}
	})
}

func TestConnSetAssignNilAssigns(t *testing.T) {
	t.Run("initializes assigns map when nil", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(50 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()

		conn, _ := newConn(ctx, wsConn, nil, "test-set-nil-assigns", opts)
		defer conn.Close()

		conn.mutex.Lock()
		conn.assigns = nil
		conn.mutex.Unlock()

		conn.SetAssign("key", "value")

		if conn.GetAssign("key") != "value" {
			t.Error("expected assigns to be initialized and value set")
		}
	})
}

func TestConnWritePumpNotActive(t *testing.T) {
	t.Run("sends close message when not active during send", func(t *testing.T) {
		closeReceived := make(chan bool, 1)

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			serverConn.SetCloseHandler(func(code int, text string) error {
				closeReceived <- true
				return nil
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
		opts.PingInterval = 1 * time.Hour

		c := &Conn{
			ID:            "test-write-inactive",
			conn:          wsConn,
			send:          make(chan []byte, 256),
			receive:       make(chan []byte, 256),
			ctx:           ctx,
			cancel:        func() {},
			closeChan:     make(chan struct{}),
			readDone:      make(chan struct{}),
			closeHandlers: newArray[func(Transport) error](),
			options:       opts,
			isClosing:     true,
		}

		c.send <- []byte(`{"test": "data"}`)

		go c.writePump()

		select {
		case <-closeReceived:
		case <-time.After(500 * time.Millisecond):
		}
	})
}

func TestConnCloseHandlerErrors(t *testing.T) {
	t.Run("reports close handler errors", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(100 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		errorReceived := make(chan string, 1)
		opts.Hooks = &Hooks{
			Metrics: &connTestMetricsCollector{errorChan: errorReceived},
		}

		conn, _ := newConn(ctx, wsConn, nil, "test-close-error", opts)

		conn.OnClose(func(t Transport) error {
			return internal("test", "close handler error")
		})

		conn.Close()

		select {
		case component := <-errorReceived:
			if component != "connection_close_handlers" {
				t.Errorf("expected component 'connection_close_handlers', got %s", component)
			}
		case <-time.After(100 * time.Millisecond):
			t.Error("expected close handler error to be reported")
		}
	})
}

func TestConnGetSendTimeout(t *testing.T) {
	t.Run("returns configured timeout", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(50 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		opts.SendTimeout = 10 * time.Second

		conn, _ := newConn(ctx, wsConn, nil, "test-get-timeout", opts)
		defer conn.Close()

		timeout := conn.getSendTimeout()
		if timeout != 10*time.Second {
			t.Errorf("expected 10s, got %v", timeout)
		}
	})

	t.Run("returns default 5s when not configured", func(t *testing.T) {
		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			time.Sleep(50 * time.Millisecond)
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		opts.SendTimeout = 0

		conn, _ := newConn(ctx, wsConn, nil, "test-get-timeout-default", opts)
		defer conn.Close()

		timeout := conn.getSendTimeout()
		if timeout != 5*time.Second {
			t.Errorf("expected 5s default, got %v", timeout)
		}
	})
}

func TestConnReadPumpBinaryMessage(t *testing.T) {
	t.Run("rejects binary messages", func(t *testing.T) {
		errorReceived := make(chan bool, 1)

		server := mockWebSocketServer(t, func(serverConn *websocket.Conn) {
			serverConn.WriteMessage(websocket.BinaryMessage, []byte{0x01, 0x02, 0x03})
			for {
				_, msg, err := serverConn.ReadMessage()
				if err != nil {
					return
				}
				if strings.Contains(string(msg), "error") {
					errorReceived <- true
				}
			}
		})
		defer server.Close()

		wsConn := createClientConn(t, server.URL)
		defer wsConn.Close()

		ctx := context.Background()
		opts := DefaultOptions()
		conn, _ := newConn(ctx, wsConn, nil, "test-binary", opts)
		defer conn.Close()

		conn.OnMessage(func(event Event, c Transport) error {
			return nil
		})
		conn.HandleMessages()

		select {
		case <-errorReceived:
		case <-time.After(500 * time.Millisecond):
		}
	})
}
